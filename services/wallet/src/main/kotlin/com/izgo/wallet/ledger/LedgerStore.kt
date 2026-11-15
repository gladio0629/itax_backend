package com.izgo.wallet.ledger

import com.izgo.wallet.domain.LedgerEntry
import com.izgo.wallet.domain.LedgerKind
import kotlinx.coroutines.reactor.awaitSingle
import org.springframework.r2dbc.core.DatabaseClient
import org.springframework.r2dbc.core.awaitOne
import org.springframework.r2dbc.core.awaitOneOrNull
import org.springframework.r2dbc.core.awaitRowsUpdated
import org.springframework.stereotype.Repository
import org.springframework.transaction.reactive.TransactionalOperator
import org.springframework.transaction.reactive.executeAndAwait
import java.time.OffsetDateTime
import java.util.UUID

/** Tranzaksiya chegarasi. Testlarda oddiy `block()` bilan almashtiriladi. */
interface TxRunner {
    suspend fun <T : Any> run(block: suspend () -> T): T
}

class R2dbcTxRunner(private val op: TransactionalOperator) : TxRunner {
    override suspend fun <T : Any> run(block: suspend () -> T): T = op.executeAndAwait { block() }!!
}

/**
 * Daftar saqlash qatlami. DIQQAT: bu yerda ledger_entries uchun faqat INSERT va SELECT bor.
 * (DB trigger UPDATE/DELETE/TRUNCATE ni baribir bloklaydi.)
 */
interface LedgerStore {
    /** Hamyon bo'yicha tranzaksiya darajasidagi qulf: balance_after ketma-ketligi buzilmasligi uchun. */
    suspend fun lockWallet(walletId: UUID)

    /** Balans = yozuvlar yig'indisi. */
    suspend fun balance(walletId: UUID): Long

    suspend fun exists(kind: LedgerKind, referenceId: UUID): Boolean
    suspend fun findByReference(kind: LedgerKind, referenceId: UUID): LedgerEntry?
    suspend fun insert(entry: LedgerEntry, reason: String?)

    suspend fun ensureWalletForDriver(driverId: UUID): UUID
    suspend fun findWalletByDriver(driverId: UUID): UUID?
    suspend fun driverIdOfWallet(walletId: UUID): UUID?

    /** Yangi (seq kamayish tartibida) yozuvlar; [beforeSeq] berilsa undan oldingilar. */
    suspend fun list(walletId: UUID, beforeSeq: Long?, limit: Int): List<SeqEntry>
}

data class SeqEntry(val seq: Long, val entry: LedgerEntry)

@Repository
class R2dbcLedgerStore(private val db: DatabaseClient) : LedgerStore {

    override suspend fun lockWallet(walletId: UUID) {
        db.sql("SELECT pg_advisory_xact_lock(hashtextextended(CAST(:w AS text), 0)) AS l")
            .bind("w", walletId.toString())
            .fetch().awaitRowsUpdated()
    }

    override suspend fun balance(walletId: UUID): Long =
        db.sql("SELECT COALESCE(SUM(amount), 0)::bigint AS b FROM ledger_entries WHERE wallet_id = :w")
            .bind("w", walletId)
            .map { row, _ -> row.get("b", java.lang.Long::class.java)!!.toLong() }
            .awaitOne()

    override suspend fun exists(kind: LedgerKind, referenceId: UUID): Boolean =
        db.sql("SELECT 1 AS x FROM ledger_entries WHERE kind = :k AND reference_id = :r LIMIT 1")
            .bind("k", kind.wire).bind("r", referenceId)
            .map { _, _ -> 1 }
            .awaitOneOrNull() != null

    override suspend fun findByReference(kind: LedgerKind, referenceId: UUID): LedgerEntry? =
        db.sql(SELECT_COLUMNS + " FROM ledger_entries WHERE kind = :k AND reference_id = :r LIMIT 1")
            .bind("k", kind.wire).bind("r", referenceId)
            .map { row, _ -> toSeqEntry(row).entry }
            .awaitOneOrNull()

    override suspend fun insert(entry: LedgerEntry, reason: String?) {
        var spec = db.sql(
            """INSERT INTO ledger_entries (id, wallet_id, kind, amount, balance_after, reference_id, reason, created_at)
               VALUES (:id, :w, :k, :a, :ba, :ref, :reason, :ts)""",
        )
            .bind("id", entry.id).bind("w", entry.walletId).bind("k", entry.kind.wire)
            .bind("a", entry.amount).bind("ba", entry.balanceAfter).bind("ts", entry.createdAt)
        spec = if (entry.referenceId != null) spec.bind("ref", entry.referenceId) else spec.bindNull("ref", UUID::class.java)
        spec = if (reason != null) spec.bind("reason", reason) else spec.bindNull("reason", String::class.java)
        spec.fetch().awaitRowsUpdated()
    }

    override suspend fun ensureWalletForDriver(driverId: UUID): UUID {
        db.sql("INSERT INTO wallets (id, driver_id) VALUES (:id, :d) ON CONFLICT (driver_id) DO NOTHING")
            .bind("id", UUID.randomUUID()).bind("d", driverId)
            .fetch().awaitRowsUpdated()
        return findWalletByDriver(driverId) ?: error("wallet yaratilmadi: driver=$driverId")
    }

    override suspend fun findWalletByDriver(driverId: UUID): UUID? =
        db.sql("SELECT id FROM wallets WHERE driver_id = :d")
            .bind("d", driverId)
            .map { row, _ -> row.get("id", UUID::class.java)!! }
            .awaitOneOrNull()

    override suspend fun driverIdOfWallet(walletId: UUID): UUID? =
        db.sql("SELECT driver_id FROM wallets WHERE id = :w")
            .bind("w", walletId)
            .map { row, _ -> row.get("driver_id", UUID::class.java)!! }
            .awaitOneOrNull()

    override suspend fun list(walletId: UUID, beforeSeq: Long?, limit: Int): List<SeqEntry> {
        val sql = if (beforeSeq == null) {
            "$SELECT_COLUMNS FROM ledger_entries WHERE wallet_id = :w ORDER BY seq DESC LIMIT :lim"
        } else {
            "$SELECT_COLUMNS FROM ledger_entries WHERE wallet_id = :w AND seq < :before ORDER BY seq DESC LIMIT :lim"
        }
        var spec = db.sql(sql).bind("w", walletId).bind("lim", limit)
        if (beforeSeq != null) spec = spec.bind("before", beforeSeq)
        return spec.map { row, _ -> toSeqEntry(row) }.all().collectList().awaitSingle()
    }

    private fun toSeqEntry(row: io.r2dbc.spi.Row) = SeqEntry(
        seq = row.get("seq", java.lang.Long::class.java)!!.toLong(),
        entry = LedgerEntry(
            id = row.get("id", UUID::class.java)!!,
            walletId = row.get("wallet_id", UUID::class.java)!!,
            kind = LedgerKind.fromWire(row.get("kind", String::class.java)!!),
            amount = row.get("amount", java.lang.Long::class.java)!!.toLong(),
            balanceAfter = row.get("balance_after", java.lang.Long::class.java)!!.toLong(),
            referenceId = row.get("reference_id", UUID::class.java),
            createdAt = row.get("created_at", OffsetDateTime::class.java)!!,
        ),
    )

    private companion object {
        const val SELECT_COLUMNS = "SELECT seq, id, wallet_id, kind, amount, balance_after, reference_id, created_at"
    }
}
