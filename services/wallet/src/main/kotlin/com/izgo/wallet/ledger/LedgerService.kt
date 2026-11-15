package com.izgo.wallet.ledger

import com.izgo.wallet.domain.DebtPolicy
import com.izgo.wallet.domain.LedgerEntry
import com.izgo.wallet.domain.NewEntry
import com.izgo.wallet.outbox.OutboxWriter
import com.izgo.wallet.outbox.Subjects
import org.slf4j.LoggerFactory
import org.springframework.stereotype.Service
import java.time.Clock
import java.time.OffsetDateTime
import java.util.UUID

data class AppendResult(
    /** Yozilgan yozuvlar; [duplicate] bo'lsa bo'sh. */
    val entries: List<LedgerEntry>,
    val balanceBefore: Long,
    val balanceAfter: Long,
    /** Shu (kind, reference_id) allaqachon yozilgan edi — hech narsa o'zgarmadi (idempotent qayta ishlash). */
    val duplicate: Boolean,
)

/**
 * Hamyon daftari xizmati. Qoidalar:
 *  - faqat INSERT; balans doim SUM dan olinadi;
 *  - bir hamyonga parallel yozuvlar advisory qulf bilan ketma-ket (balance_after to'g'ri bo'lishi uchun);
 *  - qarz chegarasi YOZUVNI RAD ETMAYDI (komissiya doim yechiladi); u faqat yangi buyurtmani to'sadi;
 *  - chegara kesib o'tilganda wallet.limit_reached outbox ga shu tranzaksiyada yoziladi.
 */
@Service
class LedgerService(
    private val store: LedgerStore,
    private val tx: TxRunner,
    private val debt: DebtPolicy,
    private val outbox: OutboxWriter,
    private val clock: Clock = Clock.systemUTC(),
) {
    private val log = LoggerFactory.getLogger(javaClass)

    /** Alohida tranzaksiyada yozadi. Boshqa yozuvlar (masalan payment.paid) bilan birga yozish uchun [appendInTx]. */
    suspend fun append(walletId: UUID, entries: List<NewEntry>): AppendResult =
        tx.run { appendInTx(walletId, entries) }

    /** Chaqiruvchi tranzaksiyasi ICHIDA chaqirilishi shart. */
    suspend fun appendInTx(walletId: UUID, entries: List<NewEntry>): AppendResult {
        require(entries.isNotEmpty()) { "kamida bitta yozuv kerak" }
        require(entries.none { it.amount == 0L }) { "amount 0 bo'lishi mumkin emas" }

        store.lockWallet(walletId)
        val before = store.balance(walletId)

        // Qayta yuborilgan (NATS redelivery, Backoffice retry) so'rov ikki marta yozilmasin.
        for (e in entries) {
            val ref = e.referenceId ?: continue
            if (store.exists(e.kind, ref)) {
                log.info("daftar: takroriy yozuv o'tkazib yuborildi kind={} ref={}", e.kind.wire, ref)
                return AppendResult(emptyList(), before, before, duplicate = true)
            }
        }

        var running = before
        val written = ArrayList<LedgerEntry>(entries.size)
        for (e in entries) {
            running += e.amount // balans = oldingi SUM + shu yozuv; keyingi o'qishda SUM aynan shuni beradi
            val entry = LedgerEntry(
                id = UUID.randomUUID(),
                walletId = walletId,
                kind = e.kind,
                amount = e.amount,
                balanceAfter = running,
                referenceId = e.referenceId,
                createdAt = OffsetDateTime.now(clock),
            )
            store.insert(entry, e.reason)
            written += entry
        }

        if (debt.crossedLimit(before, running)) {
            val driverId = store.driverIdOfWallet(walletId)
            outbox.enqueue(
                "wallet.limit_reached",
                Subjects.WALLET_LIMIT_REACHED,
                mapOf(
                    "wallet_id" to walletId.toString(),
                    "driver_id" to driverId?.toString(),
                    "balance" to running,
                    "limit" to debt.limit,
                    "reached_at" to OffsetDateTime.now(clock).toInstant().toString(),
                ),
            )
        }
        return AppendResult(written, before, running, duplicate = false)
    }

    suspend fun balance(walletId: UUID): Long = store.balance(walletId)
}
