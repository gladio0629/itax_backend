package com.izgo.wallet

import com.izgo.wallet.domain.CommissionPolicy
import com.izgo.wallet.domain.DebtPolicy
import com.izgo.wallet.domain.DebtStatus
import org.junit.jupiter.api.Assertions.assertEquals
import org.junit.jupiter.api.Assertions.assertFalse
import org.junit.jupiter.api.Assertions.assertTrue
import org.junit.jupiter.api.Test
import org.junit.jupiter.api.assertThrows

class PoliciesTest {

    private val commission = CommissionPolicy(1000) // 10%
    private val debt = DebtPolicy(-5_000_000, 0.8)

    @Test
    fun `komissiya 10 foiz`() {
        assertEquals(1_500_000, commission.commissionFor(15_000_000)) // 150 000 so'm -> 15 000 so'm
        assertEquals(0, commission.commissionFor(0))
    }

    @Test
    fun `komissiya butun tiyinga HALF_UP yaxlitlanadi`() {
        assertEquals(1, commission.commissionFor(5))  // 0.5 -> 1
        assertEquals(0, commission.commissionFor(4))  // 0.4 -> 0
        assertEquals(11, commission.commissionFor(105)) // 10.5 -> 11
    }

    @Test
    fun `manfiy narx rad etiladi`() {
        assertThrows<IllegalArgumentException> { commission.commissionFor(-1) }
    }

    @Test
    fun `qarz holatlari`() {
        assertEquals(DebtStatus.OK, debt.status(0))
        assertEquals(DebtStatus.OK, debt.status(-3_999_999))
        assertEquals(DebtStatus.APPROACHING, debt.status(-4_000_000))
        assertEquals(DebtStatus.APPROACHING, debt.status(-4_999_999))
        assertEquals(DebtStatus.AT_LIMIT, debt.status(-5_000_000)) // chegaraning o'zi — to'sadi
        assertEquals(DebtStatus.AT_LIMIT, debt.status(-9_000_000))
    }

    @Test
    fun `chegarada buyurtma qabul qilib bolmaydi`() {
        assertTrue(debt.allowed(-4_999_999))
        assertFalse(debt.allowed(-5_000_000))
        assertFalse(debt.allowed(-5_000_001))
        assertTrue(debt.allowed(10_000))
    }

    @Test
    fun `chegara faqat kesib otilganda`() {
        assertTrue(debt.crossedLimit(-4_900_000, -5_000_000))
        assertFalse(debt.crossedLimit(-5_000_000, -5_100_000)) // allaqachon chegarada edi
        assertFalse(debt.crossedLimit(0, -100))
    }
}
