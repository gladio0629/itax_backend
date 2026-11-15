package com.izgo.wallet

import com.fasterxml.jackson.databind.ObjectMapper
import com.izgo.wallet.domain.CommissionPolicy
import com.izgo.wallet.domain.DebtPolicy
import com.izgo.wallet.domain.LedgerKind
import com.izgo.wallet.domain.NewEntry
import com.izgo.wallet.ledger.CorrectionService
import com.izgo.wallet.ledger.LedgerService
import com.izgo.wallet.trip.PoisonEventException
import com.izgo.wallet.trip.TripCompletedProcessor
import com.izgo.wallet.web.ApiException
import com.izgo.wallet.web.CorrectionRequest
import kotlinx.coroutines.test.runTest
import org.junit.jupiter.api.Assertions.assertEquals
import org.junit.jupiter.api.Assertions.assertFalse
import org.junit.jupiter.api.Assertions.assertTrue
import org.junit.jupiter.api.BeforeEach
import org.junit.jupiter.api.Test
import org.junit.jupiter.api.assertThrows
import java.util.UUID

class LedgerAndTripTest {
    private val store = InMemoryLedgerStore()
    private val outbox = RecordingOutbox()
    private val debt = DebtPolicy(-5_000_000, 0.8)
    private lateinit var ledger: LedgerService
    private lateinit var processor: TripCompletedProcessor

    private val driver = UUID.randomUUID()
    private lateinit var wallet: UUID

    @BeforeEach
    fun setUp() = runTest {
        ledger = LedgerService(store, DirectTx, debt, outbox)
        processor = TripCompletedProcessor(ledger, store, CommissionPolicy(1000), ObjectMapper())
        wallet = store.ensureWalletForDriver(driver)
    }

    private fun tripEvent(tripId: UUID, price: Long, method: String?, commission: Long = price / 10): ByteArray {
        val pm = if (method == null) "" else ",\"payment_method\":\"$method\""
        return """{"event":"trip.completed","version":1,"event_id":"${UUID.randomUUID()}","occurred_at":"2026-01-01T00:00:00Z",
            "payload":{"trip_id":"$tripId","order_id":"${UUID.randomUUID()}","driver_id":"$driver","passenger_id":"${UUID.randomUUID()}",
            "final_price":$price,"commission":$commission,"distance_m":1000,"duration_s":300,"completed_at":"2026-01-01T00:00:00Z"$pm}}"""
            .toByteArray()
    }

    // ------------------------------------------------------------ daftar

    @Test
    fun `balans yozuvlar yigindisiga teng va balance_after ketma-ket`() = runTest {
        val res = ledger.append(
            wallet,
            listOf(NewEntry(LedgerKind.TOPUP, 1_000_000, UUID.randomUUID()), NewEntry(LedgerKind.COMMISSION, -150_000, UUID.randomUUID())),
        )
        assertEquals(listOf(1_000_000L, 850_000L), res.entries.map { it.balanceAfter })
        assertEquals(850_000, store.balance(wallet))
        assertEquals(store.rows.sumOf { it.amount }, ledger.balance(wallet))
    }

    @Test
    fun `bir xil reference ikki marta yozilmaydi`() = runTest {
        val ref = UUID.randomUUID()
        ledger.append(wallet, listOf(NewEntry(LedgerKind.COMMISSION, -100, ref)))
        val again = ledger.append(wallet, listOf(NewEntry(LedgerKind.COMMISSION, -100, ref)))
        assertTrue(again.duplicate)
        assertEquals(1, store.rows.size)
    }

    @Test
    fun `nol summa rad etiladi`() = runTest {
        assertThrows<IllegalArgumentException> { ledger.append(wallet, listOf(NewEntry(LedgerKind.BONUS, 0, null))) }
    }

    @Test
    fun `limit_reached faqat chegarani kesib otganda va bir marta chiqadi`() = runTest {
        ledger.append(wallet, listOf(NewEntry(LedgerKind.COMMISSION, -4_900_000, UUID.randomUUID())))
        assertTrue(outbox.events.isEmpty())

        ledger.append(wallet, listOf(NewEntry(LedgerKind.COMMISSION, -200_000, UUID.randomUUID())))
        assertEquals(1, outbox.events.size)
        assertEquals("wallet.limit_reached", outbox.events[0].name)
        assertEquals(-5_100_000L, outbox.events[0].payload["balance"])
        assertEquals(-5_000_000L, outbox.events[0].payload["limit"])

        // chegarada turib yana yechilsa — qayta hodisa yo'q, yozuv esa RAD ETILMAYDI
        ledger.append(wallet, listOf(NewEntry(LedgerKind.COMMISSION, -100_000, UUID.randomUUID())))
        assertEquals(1, outbox.events.size)
        assertEquals(-5_200_000, store.balance(wallet))
    }

    // ------------------------------------------------------ safar / komissiya

    @Test
    fun `karta to'lovi - haydovchiga pul tushadi va 10 foiz yechiladi`() = runTest {
        val trip = UUID.randomUUID()
        processor.process(tripEvent(trip, 10_000_000, "card"))

        val kinds = store.rows.map { it.kind to it.amount }
        assertEquals(listOf(LedgerKind.TRIP_PAYMENT to 10_000_000L, LedgerKind.COMMISSION to -1_000_000L), kinds)
        assertEquals(9_000_000, store.balance(wallet))
        assertTrue(store.rows.all { it.referenceId == trip })
    }

    @Test
    fun `naqd to'lov - faqat komissiya hamyondan ayriladi`() = runTest {
        processor.process(tripEvent(UUID.randomUUID(), 10_000_000, "cash"))
        assertEquals(listOf(LedgerKind.COMMISSION), store.rows.map { it.kind })
        assertEquals(-1_000_000, store.balance(wallet))
    }

    @Test
    fun `payment_method yo'q bo'lsa naqd deb olinadi`() = runTest {
        processor.process(tripEvent(UUID.randomUUID(), 10_000_000, null))
        assertEquals(listOf(LedgerKind.COMMISSION), store.rows.map { it.kind })
    }

    @Test
    fun `takroriy trip_completed ikki marta yechmaydi`() = runTest {
        val e = tripEvent(UUID.randomUUID(), 10_000_000, "card")
        processor.process(e)
        processor.process(e)
        assertEquals(2, store.rows.size)
        assertEquals(9_000_000, store.balance(wallet))
    }

    @Test
    fun `komissiya wallet hisobidan olinadi, event qiymatidan emas`() = runTest {
        processor.process(tripEvent(UUID.randomUUID(), 10_000_000, "cash", commission = 123))
        assertEquals(-1_000_000, store.balance(wallet))
    }

    @Test
    fun `buzuq hodisa poison hisoblanadi`() = runTest {
        assertThrows<PoisonEventException> { processor.process("{}".toByteArray()) }
        assertThrows<PoisonEventException> { processor.process("not json".toByteArray()) }
    }

    @Test
    fun `qarz chegarasi - naqd safarlar balansni chegaraga olib boradi`() = runTest {
        repeat(5) { processor.process(tripEvent(UUID.randomUUID(), 10_000_000, "cash")) } // 5 x -1 000 000
        assertEquals(-5_000_000, store.balance(wallet))
        assertFalse(debt.allowed(store.balance(wallet)))
        assertEquals(1, outbox.events.count { it.name == "wallet.limit_reached" })
    }

    // ------------------------------------------------------------ tuzatish

    @Test
    fun `tuzatish sabab bilan yoziladi, qayta yuborilsa asl yozuv qaytadi`() = runTest {
        val corrections = CorrectionService(ledger, store)
        val req = CorrectionRequest(UUID.randomUUID(), wallet, 500_000, "Noto'g'ri yechilgan komissiya")

        val (first, created) = corrections.apply(req)
        assertTrue(created)
        assertEquals(LedgerKind.CORRECTION, first.kind)

        val (second, createdAgain) = corrections.apply(req)
        assertFalse(createdAgain)
        assertEquals(first.id, second.id)
        assertEquals(1, store.rows.size)
    }

    @Test
    fun `bir xil correction_id boshqa summa bilan - 409`() = runTest {
        val corrections = CorrectionService(ledger, store)
        val id = UUID.randomUUID()
        corrections.apply(CorrectionRequest(id, wallet, 500_000, "Birinchi sabab"))
        val ex = assertThrows<ApiException> { corrections.apply(CorrectionRequest(id, wallet, 600_000, "Birinchi sabab")) }
        assertEquals("IDEMPOTENCY_KEY_REUSED", ex.key)
    }

    @Test
    fun `tuzatish sababsiz yoki nol summa bilan rad etiladi`() = runTest {
        val corrections = CorrectionService(ledger, store)
        assertThrows<ApiException> { corrections.apply(CorrectionRequest(UUID.randomUUID(), wallet, 0, "Yetarli sabab")) }
        assertThrows<ApiException> { corrections.apply(CorrectionRequest(UUID.randomUUID(), wallet, 10, "   ")) }
        assertThrows<ApiException> { corrections.apply(CorrectionRequest(UUID.randomUUID(), UUID.randomUUID(), 10, "Yetarli sabab")) }
    }
}
