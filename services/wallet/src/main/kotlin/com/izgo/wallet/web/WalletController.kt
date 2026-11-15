package com.izgo.wallet.web

import com.izgo.wallet.domain.DebtPolicy
import com.izgo.wallet.domain.DebtStatus
import com.izgo.wallet.ledger.LedgerStore
import com.izgo.wallet.payment.PaymentService
import com.izgo.wallet.security.Authenticator
import jakarta.validation.Valid
import org.springframework.http.HttpStatus
import org.springframework.web.bind.annotation.GetMapping
import org.springframework.web.bind.annotation.PostMapping
import org.springframework.web.bind.annotation.RequestBody
import org.springframework.web.bind.annotation.RequestHeader
import org.springframework.web.bind.annotation.RequestMapping
import org.springframework.web.bind.annotation.RequestParam
import org.springframework.web.bind.annotation.ResponseStatus
import org.springframework.web.bind.annotation.RestController
import java.util.Base64

@RestController
class HealthController {
    @GetMapping("/healthz")
    fun health() = "ok"
}

/** Public API (Kong orqali, haydovchi JWT). Yo'llar: api/openapi.yml — Wallet guruhi. */
@RestController
@RequestMapping("/api/v1/wallet")
class WalletController(
    private val auth: Authenticator,
    private val store: LedgerStore,
    private val debt: DebtPolicy,
    private val payments: PaymentService,
) {
    @GetMapping("/balance")
    suspend fun balance(@RequestHeader("Authorization", required = false) authorization: String?): WalletBalance {
        val driverId = auth.requireDriver(authorization)
        val walletId = store.ensureWalletForDriver(driverId)
        val balance = store.balance(walletId) // balans = SUM(yozuvlar)
        val status = debt.status(balance)
        return WalletBalance(
            walletId = walletId,
            balance = balance,
            debt = maxOf(0L, -balance),
            debtLimit = -debt.limit,
            limitReached = status == DebtStatus.AT_LIMIT,
            debtStatus = status,
        )
    }

    @GetMapping("/ledger")
    suspend fun ledger(
        @RequestHeader("Authorization", required = false) authorization: String?,
        @RequestParam(required = false) cursor: String?,
        @RequestParam(required = false) limit: Int?,
    ): LedgerPage {
        val driverId = auth.requireDriver(authorization)
        val lim = limit ?: 20
        if (lim !in 1..100) throw ApiException.validation("limit", "range")
        val before = cursor?.let { decodeCursor(it) }

        val walletId = store.ensureWalletForDriver(driverId)
        val rows = store.list(walletId, before, lim + 1)
        val page = rows.take(lim)
        val next = if (rows.size > lim) encodeCursor(page.last().seq) else null
        return LedgerPage(page.map { it.entry }, next)
    }

    @PostMapping("/topup")
    @ResponseStatus(HttpStatus.CREATED)
    suspend fun topup(
        @RequestHeader("Authorization", required = false) authorization: String?,
        @RequestHeader("Idempotency-Key", required = false) idempotencyKey: String?,
        @Valid @RequestBody body: TopupRequest,
    ): TopupSession {
        val driverId = auth.requireDriver(authorization)
        return payments.topup(driverId, idempotencyKey, body)
    }

    @PostMapping("/card/link")
    @ResponseStatus(HttpStatus.CREATED)
    suspend fun linkCard(
        @RequestHeader("Authorization", required = false) authorization: String?,
        @Valid @RequestBody body: CardLinkRequest,
    ): CardLinkSession {
        val driverId = auth.requireDriver(authorization)
        return payments.linkCard(driverId, body)
    }

    private fun encodeCursor(seq: Long) =
        Base64.getUrlEncoder().withoutPadding().encodeToString(seq.toString().toByteArray())

    private fun decodeCursor(c: String): Long = try {
        String(Base64.getUrlDecoder().decode(c)).toLong().also { require(it > 0) }
    } catch (e: Exception) {
        throw ApiException.validation("cursor", "invalid")
    }
}
