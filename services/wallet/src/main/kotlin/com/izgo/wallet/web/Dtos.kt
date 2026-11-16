package com.izgo.wallet.web

import com.fasterxml.jackson.annotation.JsonValue
import com.izgo.wallet.domain.DebtStatus
import com.izgo.wallet.domain.LedgerEntry
import com.izgo.wallet.domain.Provider
import jakarta.validation.constraints.Max
import jakarta.validation.constraints.Min
import jakarta.validation.constraints.NotBlank
import jakarta.validation.constraints.Size
import java.time.OffsetDateTime
import java.util.UUID

// JSON maydon nomlari global SNAKE_CASE (spring.jackson.property-naming-strategy).

data class WalletBalance(
    val walletId: UUID,
    val balance: Long,
    val debt: Long,
    val debtLimit: Long,
    val limitReached: Boolean,
    /** OpenAPI ga qo'shimcha maydon: ok | approaching | at_limit */
    val debtStatus: DebtStatus,
    val currency: String = "UZS",
)

data class LedgerPage(val data: List<LedgerEntry>, val cursor: String?)

data class TopupRequest(
    @field:Min(100_000) @field:Max(1_000_000_000) val amount: Long,
    val provider: Provider,
)

enum class PaymentStatus(@get:JsonValue val wire: String) { PENDING("pending"), PAID("paid"), FAILED("failed") }

data class TopupSession(
    val paymentId: UUID,
    val status: PaymentStatus,
    val redirectUrl: String,
    val expiresAt: OffsetDateTime,
)

data class CardLinkRequest(val provider: Provider)

data class CardLinkSession(val linkId: UUID, val redirectUrl: String, val expiresAt: OffsetDateTime)

// ---------------------------------------------------------------- internal

data class CheckLimitRequest(val driverId: UUID)

data class CheckLimitResponse(
    val allowed: Boolean,
    val balance: Long,
    val debtStatus: DebtStatus,
    /** Qarz chegarasi (manfiy, tiyin) — trip-order WALLET_LIMIT_REACHED{limit} uchun. */
    val limit: Long,
)

enum class CallbackStatus(@get:JsonValue val wire: String) { PAID("paid"), FAILED("failed") }

/** Provayder callback'i: real Payme/Click/Hamkorbank qo'shimcha maydonlar yuboradi — bu yerda qat'iy rejim (F-09) qo'llanmaydi. */
@com.fasterxml.jackson.annotation.JsonIgnoreProperties(ignoreUnknown = true)
data class PaymentCallbackRequest(
    val paymentId: UUID,
    val provider: Provider,
    @field:NotBlank @field:Size(max = 128) val providerTxId: String,
    @field:Min(1) val amount: Long,
    val status: CallbackStatus,
)

data class PaymentCallbackResponse(val paymentId: UUID, val status: PaymentStatus)

data class CorrectionRequest(
    /** Backoffice dagi tuzatish ID si — qayta yuborish xavfsizligi (idempotentlik) shu bo'yicha. */
    val correctionId: UUID,
    val walletId: UUID,
    val amount: Long,
    @field:NotBlank @field:Size(min = 5, max = 500) val reason: String,
)
