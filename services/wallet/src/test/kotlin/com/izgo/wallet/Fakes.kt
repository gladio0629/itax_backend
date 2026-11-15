package com.izgo.wallet

import com.izgo.wallet.domain.LedgerEntry
import com.izgo.wallet.domain.LedgerKind
import com.izgo.wallet.ledger.LedgerStore
import com.izgo.wallet.ledger.SeqEntry
import com.izgo.wallet.ledger.TxRunner
import com.izgo.wallet.outbox.OutboxWriter
import java.util.UUID

/** Tranzaksiyasiz oddiy bajaruvchi (testlar uchun). */
object DirectTx : TxRunner {
    override suspend fun <T : Any> run(block: suspend () -> T): T = block()
}

/** Xotiradagi daftar: faqat INSERT; balans = SUM — haqiqiy saqlash qatlami bilan bir xil qoida. */
class InMemoryLedgerStore : LedgerStore {
    val rows = mutableListOf<LedgerEntry>()
    val wallets = mutableMapOf<UUID, UUID>() // driverId -> walletId

    override suspend fun lockWallet(walletId: UUID) = Unit
    override suspend fun balance(walletId: UUID): Long = rows.filter { it.walletId == walletId }.sumOf { it.amount }
    override suspend fun exists(kind: LedgerKind, referenceId: UUID) = rows.any { it.kind == kind && it.referenceId == referenceId }
    override suspend fun findByReference(kind: LedgerKind, referenceId: UUID) =
        rows.firstOrNull { it.kind == kind && it.referenceId == referenceId }

    override suspend fun insert(entry: LedgerEntry, reason: String?) {
        rows += entry
    }

    override suspend fun ensureWalletForDriver(driverId: UUID): UUID = wallets.getOrPut(driverId) { UUID.randomUUID() }
    override suspend fun findWalletByDriver(driverId: UUID) = wallets[driverId]
    override suspend fun driverIdOfWallet(walletId: UUID) = wallets.entries.firstOrNull { it.value == walletId }?.key
    override suspend fun list(walletId: UUID, beforeSeq: Long?, limit: Int): List<SeqEntry> = emptyList()
}

class RecordingOutbox : OutboxWriter {
    data class Event(val name: String, val subject: String, val payload: Map<String, Any?>)

    val events = mutableListOf<Event>()
    override suspend fun enqueue(name: String, subject: String, payload: Map<String, Any?>) {
        events += Event(name, subject, payload)
    }
}
