package com.izgo.wallet.trip

import com.fasterxml.jackson.databind.ObjectMapper
import com.izgo.wallet.domain.CommissionPolicy
import com.izgo.wallet.domain.LedgerKind
import com.izgo.wallet.domain.NewEntry
import com.izgo.wallet.ledger.LedgerService
import com.izgo.wallet.ledger.LedgerStore
import org.slf4j.LoggerFactory
import org.springframework.stereotype.Service
import java.util.UUID

/** Xabar tuzilishi noto'g'ri — qayta yuborishning foydasi yo'q (NATS: term). */
class PoisonEventException(message: String) : RuntimeException(message)

data class TripCompleted(
    val tripId: UUID,
    val driverId: UUID,
    val finalPrice: Long,
    val eventCommission: Long?,
    val paymentMethod: String?,
)

/**
 * trip.completed -> daftar yozuvlari (bitta tranzaksiyada):
 *  - ELEKTRON to'lov (card/wallet/payme/click): pul haydovchi hamyoniga tushadi (trip_payment +final_price),
 *    so'ng komissiya yechiladi (commission -10%);
 *  - NAQD to'lov: haydovchi pulni qo'lida oldi, faqat komissiya hamyondagi puldan ayriladi (commission -10%).
 *  - payment_method yo'q/noma'lum => NAQD deb olinadi. Sabab: elektron deb faraz qilsak, platforma
 *    olmagan pulni haydovchiga yozib qo'yamiz (xavfli xato); naqd deb faraz qilsak eng ko'pi komissiya olinadi.
 *
 * Idempotentlik: (kind, trip_id) bo'yicha DB unique indeks + LedgerService tekshiruvi.
 */
@Service
class TripCompletedProcessor(
    private val ledger: LedgerService,
    private val store: LedgerStore,
    private val commission: CommissionPolicy,
    private val om: ObjectMapper,
) {
    private val log = LoggerFactory.getLogger(javaClass)

    suspend fun process(raw: ByteArray) {
        val trip = parse(raw)
        val fee = commission.commissionFor(trip.finalPrice)
        if (trip.eventCommission != null && trip.eventCommission != fee) {
            log.warn(
                "komissiya farqi trip={} event.commission={} wallet.hisobi={} — wallet hisobi ishlatiladi",
                trip.tripId, trip.eventCommission, fee,
            )
        }

        val entries = buildList {
            if (isElectronic(trip.paymentMethod) && trip.finalPrice > 0) {
                add(NewEntry(LedgerKind.TRIP_PAYMENT, trip.finalPrice, trip.tripId))
            }
            if (fee > 0) add(NewEntry(LedgerKind.COMMISSION, -fee, trip.tripId))
        }
        if (entries.isEmpty()) return

        val walletId = store.ensureWalletForDriver(trip.driverId)
        val res = ledger.append(walletId, entries)
        log.info(
            "trip.completed qayta ishlandi trip={} method={} duplicate={} balance {} -> {}",
            trip.tripId, trip.paymentMethod ?: "(yo'q=naqd)", res.duplicate, res.balanceBefore, res.balanceAfter,
        )
    }

    private fun parse(raw: ByteArray): TripCompleted {
        try {
            val root = om.readTree(raw)
            if (root.path("event").asText() != "trip.completed") throw PoisonEventException("event != trip.completed")
            val p = root.path("payload")
            val finalPrice = p.path("final_price")
            if (!finalPrice.isIntegralNumber || finalPrice.asLong() < 0) throw PoisonEventException("final_price noto'g'ri")
            return TripCompleted(
                tripId = UUID.fromString(p.path("trip_id").asText()),
                driverId = UUID.fromString(p.path("driver_id").asText()),
                finalPrice = finalPrice.asLong(),
                eventCommission = p.path("commission").takeIf { it.isIntegralNumber }?.asLong(),
                paymentMethod = p.path("payment_method").takeIf { it.isTextual }?.asText(),
            )
        } catch (e: PoisonEventException) {
            throw e
        } catch (e: Exception) {
            throw PoisonEventException("trip.completed o'qib bo'lmadi: ${e.message}")
        }
    }

    companion object {
        private val ELECTRONIC = setOf("card", "wallet", "payme", "click")
        fun isElectronic(method: String?): Boolean = method?.lowercase() in ELECTRONIC
    }
}
