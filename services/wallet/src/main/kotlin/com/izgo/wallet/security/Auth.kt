package com.izgo.wallet.security

import com.fasterxml.jackson.databind.JsonNode
import com.fasterxml.jackson.databind.ObjectMapper
import com.izgo.wallet.config.WalletProperties
import com.izgo.wallet.web.ApiException
import kotlinx.coroutines.reactor.awaitSingle
import org.springframework.data.redis.core.ReactiveStringRedisTemplate
import org.springframework.stereotype.Component
import java.nio.charset.StandardCharsets
import java.security.MessageDigest
import java.security.PublicKey
import java.security.Signature
import java.time.Clock
import java.util.Base64
import java.util.UUID

data class DriverIdentity(val userId: UUID, val sessionId: String)

/**
 * Identity chiqargan access tokenni tekshiradi (trip-order/authn bilan bir xil qoidalar):
 * EdDSA(Ed25519) imzo, issuer, muddat, typ=access, yagona audience=driver va role=driver.
 * Faqat public kalit kerak.
 */
class JwtVerifier(
    private val publicKey: PublicKey,
    private val issuer: String,
    private val clock: Clock = Clock.systemUTC(),
    private val om: ObjectMapper = ObjectMapper(),
) {
    /** Yaroqsiz bo'lsa null. Blacklist alohida tekshiriladi ([Authenticator]). */
    fun verify(token: String): DriverIdentity? {
        try {
            val parts = token.split(".")
            if (parts.size != 3) return null
            val dec = Base64.getUrlDecoder()
            val header = om.readTree(dec.decode(parts[0]))
            if (header.path("alg").asText() != "EdDSA") return null

            val sig = Signature.getInstance("Ed25519")
            sig.initVerify(publicKey)
            sig.update("${parts[0]}.${parts[1]}".toByteArray(StandardCharsets.US_ASCII))
            if (!sig.verify(dec.decode(parts[2]))) return null

            val c = om.readTree(dec.decode(parts[1]))
            val exp = c.path("exp")
            val sid = c.path("sid").asText("")
            val valid = c.path("iss").asText() == issuer &&
                exp.isNumber && exp.asLong() > clock.instant().epochSecond &&
                c.path("typ").asText() == "access" && sid.isNotEmpty() &&
                audience(c.path("aud")) == "driver" && c.path("role").asText() == "driver"
            return if (valid) DriverIdentity(UUID.fromString(c.path("sub").asText()), sid) else null
        } catch (e: Exception) {
            return null
        }
    }

    private fun audience(n: JsonNode): String? = when {
        n.isTextual -> n.asText()
        n.isArray && n.size() == 1 -> n[0].asText()
        else -> null
    }
}

@Component
class Authenticator(
    private val verifier: JwtVerifier,
    private val redis: ReactiveStringRedisTemplate,
    private val props: WalletProperties,
) {
    /** Public endpointlar: haydovchi tokeni. Redis xatosi -> 500 (fail-closed). */
    suspend fun requireDriver(authorization: String?): UUID {
        val token = authorization?.takeIf { it.startsWith("Bearer ", ignoreCase = true) }?.substring(7)?.trim()
            ?: throw ApiException.unauthorized()
        val id = verifier.verify(token) ?: throw ApiException.unauthorized()
        if (redis.hasKey("bl:sid:${id.sessionId}").awaitSingle()) throw ApiException.unauthorized()
        return id.userId
    }

    /** /internal/ yo'llari: servislararo umumiy sir (X-Internal-Token), doimiy vaqtda solishtiriladi. */
    fun requireInternal(token: String?) {
        val allowed = props.internalTokenList
        val got = (token ?: "").toByteArray(StandardCharsets.UTF_8)
        var ok = false
        for (t in allowed) ok = MessageDigest.isEqual(got, t.toByteArray(StandardCharsets.UTF_8)) || ok
        if (!ok) throw ApiException.unauthorized()
    }
}
