package com.izgo.wallet.domain

import com.fasterxml.jackson.annotation.JsonValue
import java.time.OffsetDateTime
import java.util.UUID

/**
 * Daftar yozuvi turlari. `bonus` keyinroq ishlatiladi (hozir faqat sxemada).
 * Diqqat: api/openapi.yml dagi LedgerEntry.kind enum shu ro'yxatga moslashtirilgan.
 */
enum class LedgerKind(@get:JsonValue val wire: String) {
    TRIP_PAYMENT("trip_payment"),
    COMMISSION("commission"),
    TOPUP("topup"),
    CORRECTION("correction"),
    BONUS("bonus");

    companion object {
        fun fromWire(s: String): LedgerKind = entries.first { it.wire == s }
    }
}

enum class DebtStatus(@get:JsonValue val wire: String) {
    OK("ok"),
    APPROACHING("approaching"),
    AT_LIMIT("at_limit"),
}

enum class Provider(@get:JsonValue val wire: String) {
    PAYME("payme"),
    CLICK("click");

    companion object {
        fun fromWire(s: String): Provider = entries.first { it.wire == s }
    }
}

/** Daftar yozuvi (API ga ham shu shaklda chiqadi; `reason` ichki). Pul — tiyinda, ishorali. */
data class LedgerEntry(
    val id: UUID,
    val walletId: UUID,
    val kind: LedgerKind,
    val amount: Long,
    val balanceAfter: Long,
    val referenceId: UUID?,
    val createdAt: OffsetDateTime,
)

/** Yozilishi kerak bo'lgan yangi yozuv (id, balance_after va vaqtni LedgerService qo'yadi). */
data class NewEntry(
    val kind: LedgerKind,
    val amount: Long,
    val referenceId: UUID?,
    val reason: String? = null,
)

/**
 * Qarz chegarasi. [limit] manfiy (-5 000 000 tiyin = -50 000 so'm).
 * balance <= limit  -> at_limit (yangi buyurtma olinmaydi)
 * balance <= limit * ratio -> approaching
 */
class DebtPolicy(val limit: Long, approachingRatio: Double) {
    init {
        require(limit < 0) { "debt limit manfiy bo'lishi kerak" }
        require(approachingRatio in 0.0..1.0) { "approaching ratio 0..1 oralig'ida bo'lishi kerak" }
    }

    val approachingThreshold: Long = (limit * approachingRatio).toLong()

    fun status(balance: Long): DebtStatus = when {
        balance <= limit -> DebtStatus.AT_LIMIT
        balance <= approachingThreshold -> DebtStatus.APPROACHING
        else -> DebtStatus.OK
    }

    fun allowed(balance: Long): Boolean = balance > limit

    /** Balans shu yozuvlar natijasida chegarani kesib o'tdimi (oldin chegaradan yuqori edi, endi unda yoki pastda). */
    fun crossedLimit(before: Long, after: Long): Boolean = before > limit && after <= limit
}

/**
 * Komissiya: bazis punktlarda (1000 = 10%). Butun tiyinga HALF_UP yaxlitlanadi.
 * Keyinroq daraja bo'yicha o'zgarishi uchun shu sinf almashtiriladi.
 */
class CommissionPolicy(private val bps: Long) {
    init {
        require(bps in 0..10_000) { "commission bps 0..10000 oralig'ida bo'lishi kerak" }
    }

    fun commissionFor(finalPrice: Long): Long {
        require(finalPrice >= 0) { "final_price manfiy bo'lishi mumkin emas" }
        return (Math.multiplyExact(finalPrice, bps) + 5_000) / 10_000
    }
}
