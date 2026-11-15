package com.izgo.wallet

import com.izgo.wallet.config.WalletProperties
import com.izgo.wallet.domain.LedgerKind
import com.izgo.wallet.domain.Provider
import com.izgo.wallet.ledger.AppendResult
import com.izgo.wallet.ledger.LedgerService
import com.izgo.wallet.ledger.LedgerStore
import com.izgo.wallet.outbox.OutboxWriter
import com.izgo.wallet.payment.CallbackVerifier
import com.izgo.wallet.payment.IdemCache
import com.izgo.wallet.payment.PaymentProvider
import com.izgo.wallet.payment.PaymentRecord
import com.izgo.wallet.payment.PaymentService
import com.izgo.wallet.payment.PaymentStore
import com.izgo.wallet.web.ApiException
import com.izgo.wallet.web.CallbackStatus
import com.izgo.wallet.web.PaymentCallbackRequest
import com.izgo.wallet.web.PaymentStatus
import com.izgo.wallet.web.TopupRequest
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.every
import io.mockk.mockk
import kotlinx.coroutines.test.runTest
import org.junit.jupiter.api.Assertions.assertEquals
import org.junit.jupiter.api.BeforeEach
import org.junit.jupiter.api.Test
import org.junit.jupiter.api.assertThrows
import java.time.Clock
import java.time.OffsetDateTime
import java.time.ZoneOffset
import java.util.UUID

class MapCache : IdemCache {
    val map = mutableMapOf<String, String>()
    override suspend fun get(key: String) = map[key]
    override suspend fun put(key: String, value: String) { map[key] = value }
}

class PaymentServiceTest {
    private val store = mockk<PaymentStore>()
    private val ledgerStore = mockk<LedgerStore>()
    private val ledger = mockk<LedgerService>()
    private val outbox = mockk<OutboxWriter>(relaxed = true)
    private val provider = mockk<PaymentProvider>()
    private val verifier = mockk<CallbackVerifier>()
    private val cache = MapCache()
    private val clock = Clock.fixed(java.time.Instant.parse("2026-01-01T00:00:00Z"), ZoneOffset.UTC)

    private val user = UUID.randomUUID()
    private val wallet = UUID.randomUUID()
    private val key = UUID.randomUUID().toString()
    private lateinit var service: PaymentService

    @BeforeEach
    fun setUp() {
        service = PaymentService(store, ledgerStore, ledger, DirectTx, outbox, provider, verifier, cache, WalletProperties(), clock)
        every { provider.settlesImmediately } returns true
        every { provider.eventProviderName(any()) } returns "mock"
        every { verifier.verify(any(), any(), any()) } returns true
    }

    private fun record(amount: Long, status: String, provider: Provider = Provider.PAYME) = PaymentRecord(
        id = UUID.randomUUID(), walletId = wallet, userId = user, provider = provider, amount = amount, status = status,
        requestHash = PaymentService.sha256("topup|$amount|${provider.wire}"), providerTxId = null,
        redirectUrl = "http://mock", expiresAt = OffsetDateTime.now(clock).plusMinutes(30),
    )

    // ------------------------------------------------------- Idempotency-Key

    @Test
    fun `Idempotency-Key bo'lmasa yoki UUID bo'lmasa rad etiladi`() = runTest {
        val req = TopupRequest(1_000_000, Provider.PAYME)
        assertEquals("VALIDATION_ERROR", assertThrows<ApiException> { service.topup(user, null, req) }.key)
        assertEquals("VALIDATION_ERROR", assertThrows<ApiException> { service.topup(user, "  ", req) }.key)
        assertEquals("VALIDATION_ERROR", assertThrows<ApiException> { service.topup(user, "abc", req) }.key)
    }

    @Test
    fun `bir xil kalit va bir xil tana - asl javob qaytadi, yangi to'lov yaratilmaydi`() = runTest {
        val existing = record(1_000_000, "paid")
        coEvery { store.findByKey(user, key) } returns existing
        coEvery { store.findById(existing.id) } returns existing

        val s = service.topup(user, key, TopupRequest(1_000_000, Provider.PAYME))

        assertEquals(existing.id, s.paymentId)
        assertEquals(PaymentStatus.PAID, s.status)
        coVerify(exactly = 0) { store.insertIfAbsent(any(), any()) }
    }

    @Test
    fun `bir xil kalit boshqa tana - 409 IDEMPOTENCY_KEY_REUSED`() = runTest {
        coEvery { store.findByKey(user, key) } returns record(1_000_000, "paid")

        val ex = assertThrows<ApiException> { service.topup(user, key, TopupRequest(2_000_000, Provider.PAYME)) }

        assertEquals("IDEMPOTENCY_KEY_REUSED", ex.key)
        assertEquals(409, ex.status.value())
        coVerify(exactly = 0) { store.insertIfAbsent(any(), any()) }
    }

    @Test
    fun `mock topup callback bilan bir xil yo'ldan o'tadi - daftarga topup va payment_paid yoziladi`() = runTest {
        coEvery { store.findByKey(user, key) } returns null
        coEvery { ledgerStore.ensureWalletForDriver(user) } returns wallet
        coEvery { provider.checkoutUrl(any(), any(), any()) } returns "http://mock"
        coEvery { store.insertIfAbsent(any(), key) } returns true
        val paid = record(1_000_000, "paid")
        coEvery { store.claimPaid(any(), any()) } returns paid
        coEvery { store.findById(any()) } returns paid
        coEvery { ledger.appendInTx(any(), any()) } returns AppendResult(emptyList(), 0, 1_000_000, false)

        service.topup(user, key, TopupRequest(1_000_000, Provider.PAYME))

        coVerify(exactly = 1) {
            ledger.appendInTx(wallet, match { it.single().kind == LedgerKind.TOPUP && it.single().amount == 1_000_000L })
        }
        coVerify(exactly = 1) { outbox.enqueue("payment.paid", any(), any()) }
    }

    // ---------------------------------------------------------------- callback

    private fun cb(p: PaymentRecord, amount: Long = p.amount, status: CallbackStatus = CallbackStatus.PAID) =
        PaymentCallbackRequest(p.id, p.provider, "tx-1", amount, status)

    @Test
    fun `callback Idempotency-Key siz rad etiladi`() = runTest {
        val p = record(1_000_000, "pending")
        assertEquals("VALIDATION_ERROR", assertThrows<ApiException> { service.handleCallback(null, "sig", "1.1.1.1", cb(p)) }.key)
    }

    @Test
    fun `callback imzo noto'g'ri bo'lsa 403`() = runTest {
        every { verifier.verify(any(), any(), any()) } returns false
        val p = record(1_000_000, "pending")
        val ex = assertThrows<ApiException> { service.handleCallback("k1", "bad", "1.1.1.1", cb(p)) }
        assertEquals(403, ex.status.value())
    }

    @Test
    fun `callback summasi mos kelmasa AMOUNT_MISMATCH va daftarga yozilmaydi`() = runTest {
        val p = record(1_000_000, "pending")
        coEvery { store.findById(p.id) } returns p

        val ex = assertThrows<ApiException> { service.handleCallback("k1", "sig", "1.1.1.1", cb(p, amount = 900_000)) }

        assertEquals("AMOUNT_MISMATCH", ex.key)
        coVerify(exactly = 0) { ledger.appendInTx(any(), any()) }
    }

    @Test
    fun `provayder tasdiqlagan summa boshqacha bo'lsa AMOUNT_MISMATCH`() = runTest {
        val p = record(1_000_000, "pending")
        coEvery { store.findById(p.id) } returns p
        coEvery { provider.confirmAmount(p.id, "tx-1", p.provider, p.amount) } returns 500_000

        val ex = assertThrows<ApiException> { service.handleCallback("k1", "sig", "1.1.1.1", cb(p)) }

        assertEquals("AMOUNT_MISMATCH", ex.key)
        coVerify(exactly = 0) { ledger.appendInTx(any(), any()) }
    }

    @Test
    fun `takroriy callback rad etiladi - ikkinchi marta daftarga yozilmaydi`() = runTest {
        val p = record(1_000_000, "pending")
        coEvery { store.findById(p.id) } returns p
        coEvery { provider.confirmAmount(p.id, "tx-1", p.provider, p.amount) } returns p.amount
        coEvery { store.insertReceipt(p.provider, "k1", p.id) } returnsMany listOf(true, false)
        coEvery { store.claimPaid(p.id, "tx-1") } returns p
        coEvery { ledger.appendInTx(any(), any()) } returns AppendResult(emptyList(), 0, p.amount, false)

        service.handleCallback("k1", "sig", "1.1.1.1", cb(p)) // birinchisi o'tadi
        cache.map.clear() // Redis yo'qolgan holat: himoya DB (callback_receipts) da qolishi kerak

        val ex = assertThrows<ApiException> { service.handleCallback("k1", "sig", "1.1.1.1", cb(p)) }
        assertEquals("DUPLICATE_CALLBACK", ex.key)
        assertEquals(409, ex.status.value())
        coVerify(exactly = 1) { ledger.appendInTx(any(), any()) }
    }

    @Test
    fun `Redis keshida bor kalit DB ga yetmasdan rad etiladi`() = runTest {
        val p = record(1_000_000, "pending")
        cache.map["idem:cb:${p.provider.wire}:k9"] = "1"

        val ex = assertThrows<ApiException> { service.handleCallback("k9", "sig", "1.1.1.1", cb(p)) }

        assertEquals("DUPLICATE_CALLBACK", ex.key)
        coVerify(exactly = 0) { store.findById(any()) }
    }
}
