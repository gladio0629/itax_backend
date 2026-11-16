package com.izgo.wallet.outbox

import com.izgo.wallet.config.WalletProperties
import com.izgo.wallet.trip.PoisonEventException
import com.izgo.wallet.trip.TripCompletedProcessor
import io.nats.client.Connection
import io.nats.client.JetStream
import io.nats.client.JetStreamApiException
import io.nats.client.Message
import io.nats.client.Nats
import io.nats.client.Options
import io.nats.client.PublishOptions
import io.nats.client.PushSubscribeOptions
import io.nats.client.api.ConsumerConfiguration
import io.nats.client.api.StorageType
import io.nats.client.api.StreamConfiguration
import jakarta.annotation.PreDestroy
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.slf4j.LoggerFactory
import org.springframework.boot.context.event.ApplicationReadyEvent
import org.springframework.context.event.EventListener
import org.springframework.stereotype.Component
import java.io.FileInputStream
import java.security.KeyStore
import java.security.cert.CertificateFactory
import java.time.Duration
import javax.net.ssl.SSLContext
import javax.net.ssl.TrustManagerFactory

/** NATS ulanishi (sekin ulanadi: NATS hali ko'tarilmagan bo'lsa ham servis ishga tushadi). */
@Component
class NatsConnection(private val props: WalletProperties) {
    private val log = LoggerFactory.getLogger(javaClass)
    @Volatile private var conn: Connection? = null

    val enabled: Boolean get() = props.nats.enabled

    @Synchronized
    fun connection(): Connection {
        conn?.takeIf { it.status == Connection.Status.CONNECTED }?.let { return it }
        val b = Options.Builder().server(props.nats.url).maxReconnects(-1).connectionTimeout(Duration.ofSeconds(5))
        // F-01: o'z foydalanuvchisi (faqat izgo.payment.>, izgo.wallet.> ga yoza oladi) va TLS (dev CA)
        if (props.nats.user.isNotBlank()) b.userInfo(props.nats.user, props.nats.password)
        if (props.nats.caFile.isNotBlank()) b.sslContext(sslContextFor(props.nats.caFile))
        val c = Nats.connect(b.build())
        log.info("NATS ulandi: {}", props.nats.url)
        conn = c
        return c
    }

    fun jetStream(): JetStream = connection().jetStream()

    /** PEM CA (secrets/mtls/<servis>/ca.crt) ga ishonadigan TLS konteksti. */
    private fun sslContextFor(caFile: String): SSLContext {
        val ca = FileInputStream(caFile).use { CertificateFactory.getInstance("X.509").generateCertificate(it) }
        val ks = KeyStore.getInstance(KeyStore.getDefaultType()).apply { load(null, null); setCertificateEntry("izgo-ca", ca) }
        val tmf = TrustManagerFactory.getInstance(TrustManagerFactory.getDefaultAlgorithm()).apply { init(ks) }
        return SSLContext.getInstance("TLS").apply { init(null, tmf.trustManagers, null) }
    }

    @PreDestroy
    fun close() {
        runCatching { conn?.close() }
    }

    companion object {
        const val WALLET_STREAM = "WALLET_EVENTS"
    }
}

/**
 * Outbox -> NATS JetStream. Nats-Msg-Id = event_id (JetStream takrorlarni dedupe qiladi).
 * Yuborilgach published_at belgilanadi (outbox UPDATE ruxsat: bu daftar emas).
 */
@Component
class OutboxRelay(
    private val db: org.springframework.r2dbc.core.DatabaseClient,
    private val tx: com.izgo.wallet.ledger.TxRunner,
    private val nats: NatsConnection,
) {
    private val log = LoggerFactory.getLogger(javaClass)
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    @Volatile private var streamReady = false

    @EventListener(ApplicationReadyEvent::class)
    fun start() {
        if (!nats.enabled) {
            log.warn("NATS o'chirilgan: outbox yuborilmaydi")
            return
        }
        scope.launch {
            while (true) {
                try {
                    val n = drainOnce()
                    if (n == 0) delay(1_000)
                } catch (e: kotlinx.coroutines.CancellationException) {
                    throw e
                } catch (e: Exception) {
                    log.warn("outbox relay xatosi (qayta uriniladi): {}", e.toString())
                    delay(3_000)
                }
            }
        }
    }

    private suspend fun drainOnce(): Int = tx.run {
        val rows = OutboxRows.lockBatch(db, 50)
        for (r in rows) {
            publish(r.subject, r.id.toString(), r.payload)
            OutboxRows.markPublished(db, r.id)
        }
        rows.size
    }

    private suspend fun publish(subject: String, msgId: String, payload: String) = withContext(Dispatchers.IO) {
        val js = nats.jetStream()
        ensureStream()
        js.publish(subject, payload.toByteArray(Charsets.UTF_8), PublishOptions.builder().messageId(msgId).build())
    }

    private fun ensureStream() {
        if (streamReady) return
        val jsm = nats.connection().jetStreamManagement()
        try {
            jsm.getStreamInfo(NatsConnection.WALLET_STREAM)
        } catch (e: JetStreamApiException) {
            if (e.errorCode != 404) throw e
            jsm.addStream(
                StreamConfiguration.builder()
                    .name(NatsConnection.WALLET_STREAM)
                    .subjects("izgo.payment.>", "izgo.wallet.>")
                    .storageType(StorageType.File)
                    .maxAge(Duration.ofDays(7))
                    .duplicateWindow(Duration.ofMinutes(10))
                    .build(),
            )
        }
        streamReady = true
    }

    @PreDestroy
    fun stop() = scope.cancel()
}

/**
 * izgo.trip.completed.v1 ni TRIP_ORDER_EVENTS streamidan tinglaydi (durable, queue group — bir nechta nusxa bo'lsa
 * bitta xabar bitta nusxaga). Ack FAQAT daftar tranzaksiyasi commit bo'lgandan keyin.
 */
@Component
class TripCompletedConsumer(
    private val nats: NatsConnection,
    private val processor: TripCompletedProcessor,
) {
    private val log = LoggerFactory.getLogger(javaClass)
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)

    @EventListener(ApplicationReadyEvent::class)
    fun start() {
        if (!nats.enabled) return
        scope.launch {
            // Trip-order stream'ni birinchi publish'da yaratadi — shuncha kutamiz.
            while (true) {
                try {
                    if (subscribe()) return@launch
                } catch (e: kotlinx.coroutines.CancellationException) {
                    throw e
                } catch (e: Exception) {
                    log.warn("trip.completed obunasi muvaffaqiyatsiz (qayta uriniladi): {}", e.toString())
                }
                delay(5_000)
            }
        }
    }

    private fun subscribe(): Boolean {
        val c = nats.connection()
        val streams = c.jetStreamManagement().getStreamNames(Subjects.TRIP_COMPLETED)
        val stream = streams.firstOrNull() ?: run {
            log.info("{} uchun stream hali yo'q, kutamiz", Subjects.TRIP_COMPLETED)
            return false
        }
        val dispatcher = c.createDispatcher()
        val opts = PushSubscribeOptions.builder()
            .stream(stream)
            .durable(DURABLE)
            .configuration(ConsumerConfiguration.builder().ackWait(Duration.ofSeconds(30)).build())
            .build()
        c.jetStream().subscribe(Subjects.TRIP_COMPLETED, QUEUE, dispatcher, { msg -> scope.launch { handle(msg) } }, false, opts)
        log.info("trip.completed ga obuna bo'ldik stream={} durable={}", stream, DURABLE)
        return true
    }

    private suspend fun handle(msg: Message) {
        try {
            processor.process(msg.data)
            msg.ack() // commit'dan keyin
        } catch (e: PoisonEventException) {
            log.error("yaroqsiz trip.completed (term): {}", e.message)
            msg.term()
        } catch (e: kotlinx.coroutines.CancellationException) {
            throw e
        } catch (e: Exception) {
            log.warn("trip.completed qayta ishlanmadi, keyinroq qayta yuboriladi: {}", e.toString())
            msg.nakWithDelay(Duration.ofSeconds(5))
        }
    }

    @PreDestroy
    fun stop() = scope.cancel()

    private companion object {
        const val DURABLE = "wallet-trip-completed"
        const val QUEUE = "wallet"
    }
}
