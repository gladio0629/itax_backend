package com.izgo.wallet.outbox

import com.fasterxml.jackson.databind.ObjectMapper
import kotlinx.coroutines.reactor.awaitSingle
import org.springframework.r2dbc.core.DatabaseClient
import org.springframework.r2dbc.core.awaitRowsUpdated
import org.springframework.stereotype.Component
import java.time.Instant
import java.util.UUID

object Subjects {
    const val PAYMENT_PAID = "izgo.payment.paid.v1"
    const val WALLET_LIMIT_REACHED = "izgo.wallet.limit_reached.v1"
    const val TRIP_COMPLETED = "izgo.trip.completed.v1"
}

/**
 * Transactional outbox: hodisa biznes o'zgarish bilan BIR tranzaksiyada yoziladi
 * (chaqiruvchi tranzaksiyasi ichida chaqiriladi), keyin [OutboxRelay] NATS ga yuboradi.
 */
interface OutboxWriter {
    /** @param name hodisa nomi (masalan "payment.paid"), @param payload shared/events sxemasiga mos. */
    suspend fun enqueue(name: String, subject: String, payload: Map<String, Any?>)
}

data class OutboxRow(val id: UUID, val subject: String, val payload: String)

/** Relay uchun: yuborilmagan qatorlarni qulflab oladi (SKIP LOCKED — bir nechta nusxa bir-biriga xalaqit bermaydi). */
object OutboxRows {
    suspend fun lockBatch(db: DatabaseClient, limit: Int): List<OutboxRow> =
        db.sql(
            """SELECT id, subject, payload FROM outbox WHERE published_at IS NULL
               ORDER BY created_at LIMIT :lim FOR UPDATE SKIP LOCKED""",
        )
            .bind("lim", limit)
            .map { row, _ ->
                OutboxRow(row.get("id", UUID::class.java)!!, row.get("subject", String::class.java)!!, row.get("payload", String::class.java)!!)
            }
            .all().collectList().awaitSingle()

    suspend fun markPublished(db: DatabaseClient, id: UUID) {
        db.sql("UPDATE outbox SET published_at = now() WHERE id = :id").bind("id", id).fetch().awaitRowsUpdated()
    }
}

@Component
class R2dbcOutboxWriter(private val db: DatabaseClient, private val om: ObjectMapper) : OutboxWriter {
    override suspend fun enqueue(name: String, subject: String, payload: Map<String, Any?>) {
        val id = UUID.randomUUID()
        val body = om.writeValueAsString(
            linkedMapOf(
                "event" to name,
                "version" to 1,
                "event_id" to id.toString(),
                "occurred_at" to Instant.now().toString(),
                "payload" to payload,
            ),
        )
        db.sql("INSERT INTO outbox (id, subject, payload) VALUES (:id, :s, :p)")
            .bind("id", id).bind("s", subject).bind("p", body)
            .fetch().awaitRowsUpdated()
    }
}
