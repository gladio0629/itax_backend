package com.izgo.wallet.web

import com.izgo.wallet.domain.DebtPolicy
import com.izgo.wallet.ledger.CorrectionService
import com.izgo.wallet.ledger.LedgerStore
import com.izgo.wallet.domain.LedgerEntry
import com.izgo.wallet.payment.PaymentService
import com.izgo.wallet.security.Authenticator
import jakarta.validation.Valid
import org.springframework.http.HttpStatus
import org.springframework.http.ResponseEntity
import org.springframework.web.bind.annotation.PostMapping
import org.springframework.web.bind.annotation.RequestBody
import org.springframework.web.bind.annotation.RequestHeader
import org.springframework.web.bind.annotation.RequestMapping
import org.springframework.web.bind.annotation.RestController
import org.springframework.web.server.ServerWebExchange

/**
 * Servislararo API. Kong'da YO'Q (faqat ichki tarmoq), har so'rovda X-Internal-Token majburiy.
 */
@RestController
@RequestMapping("/internal/wallet")
class InternalController(
    private val auth: Authenticator,
    private val store: LedgerStore,
    private val debt: DebtPolicy,
    private val payments: PaymentService,
    private val corrections: CorrectionService,
) {
    /**
     * F-13: Trip & Order sinxron tekshiruvi. Chegaraga yetgan haydovchi YANGI buyurtma qabul qila olmaydi;
     * allaqachon qabul qilingan buyurtma bekor bo'lmaydi (bu endpoint faqat yangi qabulda chaqiriladi).
     * Noma'lum haydovchi (hamyoni yo'q) = balans 0 => ruxsat.
     */
    @PostMapping("/check-limit")
    suspend fun checkLimit(
        @RequestHeader("X-Internal-Token", required = false) token: String?,
        @Valid @RequestBody body: CheckLimitRequest,
    ): CheckLimitResponse {
        auth.requireInternal(token)
        val walletId = store.findWalletByDriver(body.driverId)
        val balance = if (walletId == null) 0L else store.balance(walletId)
        return CheckLimitResponse(
            allowed = debt.allowed(balance),
            balance = balance,
            debtStatus = debt.status(balance),
            limit = debt.limit,
        )
    }

    /** F-14: to'lov provayderi callbacki. Idempotency-Key majburiy; takroriy callback 409 DUPLICATE_CALLBACK. */
    @PostMapping("/payment-callback")
    suspend fun paymentCallback(
        @RequestHeader("X-Internal-Token", required = false) token: String?,
        @RequestHeader("Idempotency-Key", required = false) idempotencyKey: String?,
        @RequestHeader("X-Signature", required = false) signature: String?,
        exchange: ServerWebExchange,
        @Valid @RequestBody body: PaymentCallbackRequest,
    ): PaymentCallbackResponse {
        auth.requireInternal(token)
        val ip = exchange.request.remoteAddress?.address?.hostAddress
        return payments.handleCallback(idempotencyKey, signature, ip, body)
    }

    /** Backoffice tasdiqlagan tuzatishni daftarga yozadi (ikki kishi tasdig'i Backoffice'da). */
    @PostMapping("/correction")
    suspend fun correction(
        @RequestHeader("X-Internal-Token", required = false) token: String?,
        @RequestHeader("Idempotency-Key", required = false) idempotencyKey: String?,
        @Valid @RequestBody body: CorrectionRequest,
    ): ResponseEntity<LedgerEntry> {
        auth.requireInternal(token)
        if (idempotencyKey.isNullOrBlank()) throw ApiException.validation("idempotency_key", "required")
        val (entry, created) = corrections.apply(body)
        return ResponseEntity.status(if (created) HttpStatus.CREATED else HttpStatus.OK).body(entry)
    }
}
