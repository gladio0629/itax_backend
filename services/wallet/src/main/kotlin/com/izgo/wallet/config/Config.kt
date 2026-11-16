package com.izgo.wallet.config

import com.izgo.wallet.domain.CommissionPolicy
import com.izgo.wallet.domain.DebtPolicy
import com.izgo.wallet.ledger.R2dbcTxRunner
import com.izgo.wallet.ledger.TxRunner
import com.izgo.wallet.security.JwtVerifier
import org.springframework.boot.context.properties.ConfigurationProperties
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration
import org.springframework.transaction.ReactiveTransactionManager
import org.springframework.transaction.reactive.TransactionalOperator
import java.nio.file.Files
import java.nio.file.Path
import java.security.KeyFactory
import java.security.PublicKey
import java.security.spec.X509EncodedKeySpec
import java.util.Base64

@ConfigurationProperties("wallet")
data class WalletProperties(
    val commissionBps: Long = 1000,
    val debtLimit: Long = -5_000_000,
    val approachingRatio: Double = 0.8,
    val jwt: Jwt = Jwt(),
    val internalTokens: String = "",
    val nats: Nats = Nats(),
    val payments: Payments = Payments(),
) {
    data class Jwt(val publicKeyPath: String = "/run/secrets/jwt/public.pem", val issuer: String = "izgo-identity")
    /** F-01: user/password — wallet'ning o'z NATS foydalanuvchisi; caFile — TLS CA (PEM). Bo'sh — autentifikatsiyasiz/TLS'siz. */
    data class Nats(
        val url: String = "nats://localhost:4222",
        val enabled: Boolean = true,
        val user: String = "",
        val password: String = "",
        val caFile: String = "",
    )
    data class Payments(
        val mode: String = "mock",
        val publicBaseUrl: String = "http://localhost:8000",
        val sessionTtlMinutes: Long = 30,
        val callbackAllowedIps: String = "",
    )

    val internalTokenList: List<String>
        get() = internalTokens.split(",").map { it.trim() }.filter { it.isNotEmpty() }
}

@Configuration
class WalletConfig(private val props: WalletProperties) {

    @Bean
    fun clock(): java.time.Clock = java.time.Clock.systemUTC()

    @Bean
    fun debtPolicy() = DebtPolicy(props.debtLimit, props.approachingRatio)

    @Bean
    fun commissionPolicy() = CommissionPolicy(props.commissionBps)

    @Bean
    fun transactionalOperator(tm: ReactiveTransactionManager): TransactionalOperator = TransactionalOperator.create(tm)

    @Bean
    fun txRunner(op: TransactionalOperator): TxRunner = R2dbcTxRunner(op)

    @Bean
    fun jwtVerifier(): JwtVerifier = JwtVerifier(loadEd25519PublicKey(props.jwt.publicKeyPath), props.jwt.issuer)

    private fun loadEd25519PublicKey(path: String): PublicKey {
        val pem = Files.readString(Path.of(path))
        val b64 = pem.lines().filterNot { it.startsWith("-----") }.joinToString("")
        return KeyFactory.getInstance("Ed25519").generatePublic(X509EncodedKeySpec(Base64.getMimeDecoder().decode(b64)))
    }
}
