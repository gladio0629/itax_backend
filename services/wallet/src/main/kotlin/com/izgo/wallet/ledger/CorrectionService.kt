package com.izgo.wallet.ledger

import com.izgo.wallet.domain.LedgerEntry
import com.izgo.wallet.domain.LedgerKind
import com.izgo.wallet.domain.NewEntry
import com.izgo.wallet.web.ApiException
import com.izgo.wallet.web.CorrectionRequest
import org.springframework.stereotype.Service

/**
 * Admin tuzatish yozuvi. Ikki kishi tasdig'i BACKOFFICE tomonida bajariladi; Wallet faqat TASDIQLANGAN
 * yozuvni qabul qilib, daftarga `correction` turi bilan yozadi (sabab majburiy).
 * Backoffice qayta yuborsa (correction_id bir xil) — ikkinchi yozuv yaratilmaydi, asl yozuv qaytadi.
 */
@Service
class CorrectionService(private val ledger: LedgerService, private val store: LedgerStore) {

    /** @return (yozuv, yangi yaratildimi) */
    suspend fun apply(req: CorrectionRequest): Pair<LedgerEntry, Boolean> {
        if (req.amount == 0L) throw ApiException.validation("amount", "non_zero")
        if (req.reason.isBlank() || req.reason.trim().length < 5) throw ApiException.validation("reason", "too_short")
        store.driverIdOfWallet(req.walletId) ?: throw ApiException.notFound(req.walletId)

        val res = ledger.append(
            req.walletId,
            listOf(NewEntry(LedgerKind.CORRECTION, req.amount, req.correctionId, req.reason.trim())),
        )
        if (!res.duplicate) return res.entries.single() to true

        val existing = store.findByReference(LedgerKind.CORRECTION, req.correctionId)
            ?: error("takroriy correction topilmadi: ${req.correctionId}")
        if (existing.walletId != req.walletId || existing.amount != req.amount) throw ApiException.idempotencyKeyReused()
        return existing to false
    }
}
