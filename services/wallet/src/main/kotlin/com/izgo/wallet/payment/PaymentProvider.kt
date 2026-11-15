package com.izgo.wallet.payment

import com.izgo.wallet.config.WalletProperties
import com.izgo.wallet.domain.Provider
import org.slf4j.LoggerFactory
import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty
import org.springframework.stereotype.Component
import java.util.UUID

/**
 * To'lov provayderi interfeysi. HOZIR faqat [MockPaymentProvider] ishlaydi.
 * Real kod (Payme, Click, Hamkorbank): payment/providers/RealProviders.kt — butunlay kommentda.
 * F-15: karta raqami hech qachon bizga kelmaydi — karta provayderning o'z sahifasida kiritiladi,
 * biz faqat token va oxirgi 4 raqamni olamiz.
 */
interface PaymentProvider {
    /** true: to'lov sessiyasi yaratilgach darhol "paid" (faqat mock). */
    val settlesImmediately: Boolean

    /** payment.paid hodisasidagi provider qiymati (payme|click|wallet|mock). */
    fun eventProviderName(requested: Provider): String

    /** Foydalanuvchi to'lov qiladigan provayder sahifasi URL. */
    suspend fun checkoutUrl(paymentId: UUID, amount: Long, provider: Provider): String

    /** F-14: summani provayderdan qayta tekshirish. Provayder tasdiqlagan summani (tiyin) qaytaradi. */
    suspend fun confirmAmount(paymentId: UUID, providerTxId: String, provider: Provider, expected: Long): Long

    /** Karta ulash uchun provayder sahifasi URL. */
    suspend fun cardLinkUrl(linkId: UUID, provider: Provider): String
}

/** F-14: callback imzosi va IP tekshiruvi. */
interface CallbackVerifier {
    fun verify(provider: Provider, signature: String?, remoteIp: String?): Boolean
}

@Component
@ConditionalOnProperty("wallet.payments.mode", havingValue = "mock", matchIfMissing = true)
class MockPaymentProvider(private val props: WalletProperties) : PaymentProvider {
    override val settlesImmediately = true

    override fun eventProviderName(requested: Provider) = "mock"

    override suspend fun checkoutUrl(paymentId: UUID, amount: Long, provider: Provider): String =
        "${props.payments.publicBaseUrl}/mock-pay/${provider.wire}?payment_id=$paymentId"

    // Mock: provayder DB dagi kutilgan summani tasdiqlaydi.
    override suspend fun confirmAmount(paymentId: UUID, providerTxId: String, provider: Provider, expected: Long): Long =
        expected

    override suspend fun cardLinkUrl(linkId: UUID, provider: Provider): String =
        "${props.payments.publicBaseUrl}/mock-card-link/${provider.wire}?link_id=$linkId"
}

/**
 * MOCK: imzo va IP tekshirilmaydi. Shu sababli /internal/ yo'llari faqat X-Internal-Token bilan va Kong orqali
 * tashqariga CHIQARILMAGAN holda ishlaydi. Real rejimda quyidagi tekshiruvlar yoqilishi shart (kommentda):
 */
@Component
@ConditionalOnProperty("wallet.payments.mode", havingValue = "mock", matchIfMissing = true)
class MockCallbackVerifier : CallbackVerifier {
    private val log = LoggerFactory.getLogger(javaClass)

    override fun verify(provider: Provider, signature: String?, remoteIp: String?): Boolean {
        log.debug("MOCK callback tekshiruvi: provider={} ip={}", provider, remoteIp)
        return true
    }
}

/*
 * REAL callback tekshiruvi (yoqish uchun: wallet.payments.mode=real va yuqoridagi Mock*larni o'chirish).
 *
 * @Component
 * @ConditionalOnProperty("wallet.payments.mode", havingValue = "real")
 * class RealCallbackVerifier(private val props: WalletProperties, private val click: ClickSigner) : CallbackVerifier {
 *     private val allowed = props.payments.callbackAllowedIps.split(",").map { it.trim() }.filter { it.isNotEmpty() }
 *
 *     override fun verify(provider: Provider, signature: String?, remoteIp: String?): Boolean {
 *         // 1) IP ro'yxati (provayder IP/CIDR lari .env orqali beriladi — kodda yozilmaydi)
 *         if (remoteIp == null || allowed.none { ipMatches(remoteIp, it) }) return false
 *         // 2) Imzo
 *         return when (provider) {
 *             Provider.CLICK -> click.verify(signature)        // md5(click_trans_id + service_id + SECRET + merchant_trans_id + amount + action + sign_time)
 *             Provider.PAYME -> paymeBasicAuthOk(signature)    // Authorization: Basic base64("Paycom:" + KEY), vaqt-doimiy solishtirish
 *         }
 *     }
 * }
 *
 * Eslatma: Payme (JSON-RPC: CheckPerformTransaction, PerformTransaction...) va Click (Prepare/Complete)
 * o'z protokollariga ega — real ulashda ular uchun alohida adapter endpoint yoziladi va u shu servisning
 * /internal/wallet/payment-callback ini (Idempotency-Key = provayder tranzaksiya ID) chaqiradi.
 */
