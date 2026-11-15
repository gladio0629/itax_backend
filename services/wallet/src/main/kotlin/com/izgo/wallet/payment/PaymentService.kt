package com.izgo.wallet.payment

import com.izgo.wallet.config.WalletProperties
import com.izgo.wallet.domain.LedgerKind
import com.izgo.wallet.domain.NewEntry
import com.izgo.wallet.domain.Provider
import com.izgo.wallet.ledger.LedgerService
import com.izgo.wallet.ledger.LedgerStore
import com.izgo.wallet.ledger.TxRunner
import com.izgo.wallet.outbox.OutboxWriter
import com.izgo.wallet.outbox.Subjects
import com.izgo.wallet.web.ApiException
import com.izgo.wallet.web.CallbackStatus
import com.izgo.wallet.web.CardLinkRequest
import com.izgo.wallet.web.CardLinkSession
import com.izgo.wallet.web.PaymentCallbackRequest
import com.izgo.wallet.web.PaymentCallbackResponse
import com.izgo.wallet.web.PaymentStatus
import com.izgo.wallet.web.TopupRequest
import com.izgo.wallet.web.TopupSession
import kotlinx.coroutines.reactor.awaitSingle
import kotlinx.coroutines.reactor.awaitSingleOrNull
import org.slf4j.LoggerFactory
import org.springframework.data.redis.core.ReactiveStringRedisTemplate
import org.springframework.http.HttpStatus
import org.springframework.stereotype.Service
import java.security.MessageDigest
import java.time.Clock
import java.time.Duration
import java.time.OffsetDateTime
import java.util.UUID

/** Redis: idempotentlik kalitlari KESHI. Haqiqat manbai — DB (unique cheklovlar); Redis ishlamasa ham to'g'ri ishlaydi. */
interface IdemCache {
    suspend fun get(key: String): String?
    suspend fun put(key: String, value: String)
}

@Service
class RedisIdemCache(private val redis: ReactiveStringRedisTemplate) : IdemCache {
    private val log = LoggerFactory.getLogger(javaClass)

    override suspend fun get(key: String): String? = try {
        redis.opsForValue().get(key).awaitSingleOrNull()
    } catch (e: Exception) {
        log.warn("redis idem get xatosi (DB ga tayanamiz): {}", e.toString()); null
    }

    override suspend fun put(key: String, value: String) {
        try {
            redis.opsForValue().set(key, value, TTL).awaitSingle()
        } catch (e: Exception) {
            log.warn("redis idem put xatosi (e'tiborsiz): {}", e.toString())
        }
    }

    private companion object { val TTL: Duration = Duration.ofHours(24) }
}

@Service
class PaymentService(
    private val store: PaymentStore,
    private val ledgerStore: LedgerStore,
    private val ledger: LedgerService,
    private val tx: TxRunner,
    private val outbox: OutboxWriter,
    private val provider: PaymentProvider,
    private val verifier: CallbackVerifier,
    private val cache: IdemCache,
    private val props: WalletProperties,
    private val clock: Clock,
) {
    private val log = LoggerFactory.getLogger(javaClass)

    // ------------------------------------------------------------------ topup

    suspend fun topup(userId: UUID, idempotencyKey: String?, req: TopupRequest): TopupSession {
        val key = requireUuidKey(idempotencyKey)
        val hash = sha256("topup|${req.amount}|${req.provider.wire}")
        val cacheKey = "idem:topup:$userId:$key"

        cache.get(cacheKey)?.let { cached ->
            val (h, pid) = cached.split("|", limit = 2)
            if (h != hash) throw ApiException.idempotencyKeyReused()
            store.findById(UUID.fromString(pid))?.let { return replay(it) }
        }
        store.findByKey(userId, key)?.let { return replayChecked(it, hash, cacheKey) }

        val walletId = ledgerStore.ensureWalletForDriver(userId)
        val paymentId = UUID.randomUUID()
        val record = PaymentRecord(
            id = paymentId, walletId = walletId, userId = userId, provider = req.provider, amount = req.amount,
            status = "pending", requestHash = hash, providerTxId = null,
            redirectUrl = provider.checkoutUrl(paymentId, req.amount, req.provider),
            expiresAt = OffsetDateTime.now(clock).plusMinutes(props.payments.sessionTtlMinutes),
        )
        if (!store.insertIfAbsent(record, key)) {
            // parallel bir xil kalit: g'olib yozuvni qaytaramiz
            return replayChecked(store.findByKey(userId, key) ?: error("idempotency yozuvi topilmadi"), hash, cacheKey)
        }
        if (provider.settlesImmediately) settleMock(paymentId)
        cache.put(cacheKey, "$hash|$paymentId")
        return store.findById(paymentId)!!.toSession()
    }

    private suspend fun replayChecked(p: PaymentRecord, hash: String, cacheKey: String): TopupSession {
        if (p.requestHash != hash) throw ApiException.idempotencyKeyReused()
        cache.put(cacheKey, "$hash|${p.id}")
        return replay(p)
    }

    private suspend fun replay(p: PaymentRecord): TopupSession {
        // Mock: oldingi urinish yozuvdan keyin, to'lovdan oldin uzilgan bo'lsa yakunlaymiz (idempotent).
        if (p.status == "pending" && provider.settlesImmediately) settleMock(p.id)
        return (store.findById(p.id) ?: p).toSession()
    }

    /** Mock topup ham callback bilan BIR XIL yo'ldan o'tadi ([applyPaid]). */
    private suspend fun settleMock(paymentId: UUID) {
        applyPaid(paymentId, "mock-$paymentId", receipt = null)
    }

    // --------------------------------------------------------------- callback

    suspend fun handleCallback(
        idempotencyKey: String?,
        signature: String?,
        remoteIp: String?,
        req: PaymentCallbackRequest,
    ): PaymentCallbackResponse {
        val key = idempotencyKey?.trim()?.takeIf { it.isNotEmpty() && it.length <= 128 }
            ?: throw ApiException.validation("idempotency_key", "required")

        // F-14: imzo va IP (hozir mock; real kod PaymentProvider.kt da kommentda)
        if (!verifier.verify(req.provider, signature, remoteIp)) throw ApiException.forbidden("invalid_signature")

        val cacheKey = "idem:cb:${req.provider.wire}:$key"
        if (cache.get(cacheKey) != null) throw duplicateCallback()

        val p = store.findById(req.paymentId) ?: throw ApiException.notFound(req.paymentId)
        if (p.provider != req.provider) throw ApiException.validation("provider", "mismatch")

        val status = when (req.status) {
            CallbackStatus.PAID -> {
                // F-14: summa so'rovdagi va provayderdan qayta tekshirilgan qiymat bilan DB dagi summaga teng bo'lishi shart
                if (req.amount != p.amount) throw amountMismatch(p.amount, req.amount)
                val confirmed = provider.confirmAmount(p.id, req.providerTxId, p.provider, p.amount)
                if (confirmed != p.amount) throw amountMismatch(p.amount, confirmed)
                applyPaid(p.id, req.providerTxId, receipt = req.provider to key)
                PaymentStatus.PAID
            }
            CallbackStatus.FAILED -> {
                tx.run {
                    if (!store.insertReceipt(req.provider, key, p.id)) throw duplicateCallback()
                    if (!store.markFailed(p.id)) throw duplicateCallback()
                    Unit
                }
                PaymentStatus.FAILED
            }
        }
        cache.put(cacheKey, "1") // faqat commit'dan keyin
        return PaymentCallbackResponse(p.id, status)
    }

    /**
     * pending -> paid, topup yozuvi va payment.paid outbox — BIR tranzaksiyada.
     * [receipt] (provider, Idempotency-Key) berilsa takroriy callback rad etiladi.
     */
    private suspend fun applyPaid(paymentId: UUID, providerTxId: String, receipt: Pair<Provider, String>?) {
        tx.run {
            if (receipt != null && !store.insertReceipt(receipt.first, receipt.second, paymentId)) throw duplicateCallback()
            val p = store.claimPaid(paymentId, providerTxId)
            if (p == null && receipt != null) throw duplicateCallback() // allaqachon yakunlangan
            if (p != null) {
                ledger.appendInTx(p.walletId, listOf(NewEntry(LedgerKind.TOPUP, p.amount, p.id)))
                outbox.enqueue(
                    "payment.paid",
                    Subjects.PAYMENT_PAID,
                    mapOf(
                        "payment_id" to p.id.toString(),
                        "wallet_id" to p.walletId.toString(),
                        "amount" to p.amount,
                        "provider" to provider.eventProviderName(p.provider),
                        "provider_tx_id" to providerTxId,
                        "paid_at" to OffsetDateTime.now(clock).toInstant().toString(),
                    ),
                )
                log.info("to'lov yakunlandi payment={} amount={}", p.id, p.amount)
            }
            Unit
        }
    }

    // -------------------------------------------------------------- card link

    suspend fun linkCard(userId: UUID, req: CardLinkRequest): CardLinkSession {
        val walletId = ledgerStore.ensureWalletForDriver(userId)
        val linkId = UUID.randomUUID()
        val expires = OffsetDateTime.now(clock).plusMinutes(15)
        store.insertPendingCard(linkId, walletId, userId, req.provider, expires)
        return CardLinkSession(linkId, provider.cardLinkUrl(linkId, req.provider), expires)
    }

    // ---------------------------------------------------------------- helpers

    private fun PaymentRecord.toSession() = TopupSession(
        paymentId = id,
        status = when (status) { "paid" -> PaymentStatus.PAID; "failed" -> PaymentStatus.FAILED; else -> PaymentStatus.PENDING },
        redirectUrl = redirectUrl,
        expiresAt = expiresAt,
    )

    private fun requireUuidKey(raw: String?): String {
        val v = raw?.trim().orEmpty()
        if (v.isEmpty()) throw ApiException.validation("idempotency_key", "required")
        runCatching { UUID.fromString(v) }.getOrElse { throw ApiException.validation("idempotency_key", "invalid") }
        return v.lowercase()
    }

    private fun duplicateCallback() = ApiException(HttpStatus.CONFLICT, "DUPLICATE_CALLBACK")
    private fun amountMismatch(expected: Long, got: Long) =
        ApiException(HttpStatus.CONFLICT, "AMOUNT_MISMATCH", mapOf("expected" to expected, "got" to got))

    companion object {
        fun sha256(s: String): String =
            MessageDigest.getInstance("SHA-256").digest(s.toByteArray()).joinToString("") { "%02x".format(it) }
    }
}
