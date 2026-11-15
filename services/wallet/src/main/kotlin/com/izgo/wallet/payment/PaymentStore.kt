package com.izgo.wallet.payment

import com.izgo.wallet.domain.Provider
import org.springframework.r2dbc.core.DatabaseClient
import org.springframework.r2dbc.core.awaitOneOrNull
import org.springframework.r2dbc.core.awaitRowsUpdated
import org.springframework.stereotype.Repository
import java.time.OffsetDateTime
import java.util.UUID

data class PaymentRecord(
    val id: UUID,
    val walletId: UUID,
    val userId: UUID,
    val provider: Provider,
    val amount: Long,
    val status: String, // pending | paid | failed
    val requestHash: String,
    val providerTxId: String?,
    val redirectUrl: String,
    val expiresAt: OffsetDateTime,
)

interface PaymentStore {
    /** (user_id, idempotency_key) bo'yicha bitta yozuv. false = bu kalit bilan yozuv allaqachon bor. */
    suspend fun insertIfAbsent(p: PaymentRecord, idempotencyKey: String): Boolean
    suspend fun findByKey(userId: UUID, idempotencyKey: String): PaymentRecord?
    suspend fun findById(id: UUID): PaymentRecord?

    /** pending -> paid (atomik). Allaqachon pending emas bo'lsa null. */
    suspend fun claimPaid(id: UUID, providerTxId: String): PaymentRecord?
    suspend fun markFailed(id: UUID): Boolean

    /** (provider, Idempotency-Key) bo'yicha callback qabul qilingan. false = takroriy. */
    suspend fun insertReceipt(provider: Provider, idempotencyKey: String, paymentId: UUID): Boolean

    /** F-15: faqat id/provider/status. Karta raqami (PAN) yo'q; token va last4 keyin provayder callbackidan. */
    suspend fun insertPendingCard(id: UUID, walletId: UUID, userId: UUID, provider: Provider, expiresAt: OffsetDateTime)
}

@Repository
class R2dbcPaymentStore(private val db: DatabaseClient) : PaymentStore {

    override suspend fun insertIfAbsent(p: PaymentRecord, idempotencyKey: String): Boolean =
        db.sql(
            """INSERT INTO payment_transactions
               (id, wallet_id, user_id, provider, amount, status, idempotency_key, request_hash, redirect_url, expires_at)
               VALUES (:id, :w, :u, :prov, :amt, 'pending', :key, :hash, :url, :exp)
               ON CONFLICT (user_id, idempotency_key) DO NOTHING""",
        )
            .bind("id", p.id).bind("w", p.walletId).bind("u", p.userId).bind("prov", p.provider.wire)
            .bind("amt", p.amount).bind("key", idempotencyKey).bind("hash", p.requestHash)
            .bind("url", p.redirectUrl).bind("exp", p.expiresAt)
            .fetch().awaitRowsUpdated() > 0

    override suspend fun findByKey(userId: UUID, idempotencyKey: String): PaymentRecord? =
        db.sql("$SELECT WHERE user_id = :u AND idempotency_key = :k")
            .bind("u", userId).bind("k", idempotencyKey)
            .map { row, _ -> toRecord(row) }.awaitOneOrNull()

    override suspend fun findById(id: UUID): PaymentRecord? =
        db.sql("$SELECT WHERE id = :id").bind("id", id)
            .map { row, _ -> toRecord(row) }.awaitOneOrNull()

    override suspend fun claimPaid(id: UUID, providerTxId: String): PaymentRecord? =
        db.sql(
            """UPDATE payment_transactions SET status = 'paid', paid_at = now(), provider_tx_id = :tx
               WHERE id = :id AND status = 'pending'
               RETURNING id, wallet_id, user_id, provider, amount, status, request_hash, provider_tx_id, redirect_url, expires_at""",
        )
            .bind("id", id).bind("tx", providerTxId)
            .map { row, _ -> toRecord(row) }.awaitOneOrNull()

    override suspend fun markFailed(id: UUID): Boolean =
        db.sql("UPDATE payment_transactions SET status = 'failed' WHERE id = :id AND status = 'pending'")
            .bind("id", id).fetch().awaitRowsUpdated() > 0

    override suspend fun insertReceipt(provider: Provider, idempotencyKey: String, paymentId: UUID): Boolean =
        db.sql(
            """INSERT INTO callback_receipts (provider, idempotency_key, payment_id) VALUES (:p, :k, :pid)
               ON CONFLICT (provider, idempotency_key) DO NOTHING""",
        )
            .bind("p", provider.wire).bind("k", idempotencyKey).bind("pid", paymentId)
            .fetch().awaitRowsUpdated() > 0

    override suspend fun insertPendingCard(id: UUID, walletId: UUID, userId: UUID, provider: Provider, expiresAt: OffsetDateTime) {
        db.sql("INSERT INTO cards (id, wallet_id, user_id, provider, status, expires_at) VALUES (:id, :w, :u, :p, 'pending', :exp)")
            .bind("id", id).bind("w", walletId).bind("u", userId).bind("p", provider.wire).bind("exp", expiresAt)
            .fetch().awaitRowsUpdated()
    }

    private fun toRecord(row: io.r2dbc.spi.Row) = PaymentRecord(
        id = row.get("id", UUID::class.java)!!,
        walletId = row.get("wallet_id", UUID::class.java)!!,
        userId = row.get("user_id", UUID::class.java)!!,
        provider = Provider.fromWire(row.get("provider", String::class.java)!!),
        amount = row.get("amount", java.lang.Long::class.java)!!.toLong(),
        status = row.get("status", String::class.java)!!,
        requestHash = row.get("request_hash", String::class.java)!!,
        providerTxId = row.get("provider_tx_id", String::class.java),
        redirectUrl = row.get("redirect_url", String::class.java)!!,
        expiresAt = row.get("expires_at", OffsetDateTime::class.java)!!,
    )

    private companion object {
        const val SELECT = "SELECT id, wallet_id, user_id, provider, amount, status, request_hash, provider_tx_id, " +
            "redirect_url, expires_at FROM payment_transactions"
    }
}
