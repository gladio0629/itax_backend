package com.izgo.wallet.web

import org.slf4j.LoggerFactory
import org.springframework.http.HttpStatus
import org.springframework.http.ResponseEntity
import org.springframework.web.bind.annotation.ExceptionHandler
import org.springframework.web.bind.annotation.RestControllerAdvice
import org.springframework.web.server.ResponseStatusException
import org.springframework.web.server.ServerWebInputException
import org.springframework.web.bind.support.WebExchangeBindException
import java.util.UUID

/** Xato formati: {"error":{"key":"...","params":{}}} — kalitlar shared/errors/errors.json da. */
data class ErrorBody(val error: ErrorDetail)
data class ErrorDetail(val key: String, val params: Map<String, Any?> = emptyMap())

class ApiException(
    val status: HttpStatus,
    val key: String,
    val params: Map<String, Any?> = emptyMap(),
) : RuntimeException(key) {
    companion object {
        fun unauthorized() = ApiException(HttpStatus.UNAUTHORIZED, "AUTH_TOKEN_EXPIRED")
        fun forbidden(reason: String? = null) =
            ApiException(HttpStatus.FORBIDDEN, "FORBIDDEN", if (reason == null) emptyMap() else mapOf("reason" to reason))
        fun notFound(id: Any? = null) =
            ApiException(HttpStatus.NOT_FOUND, "NOT_FOUND", if (id == null) emptyMap() else mapOf("id" to id.toString()))
        fun validation(field: String, code: String) =
            ApiException(HttpStatus.UNPROCESSABLE_ENTITY, "VALIDATION_ERROR", mapOf("fields" to mapOf(field to code)))
        fun idempotencyKeyReused() = ApiException(HttpStatus.CONFLICT, "IDEMPOTENCY_KEY_REUSED")
    }
}

@RestControllerAdvice
class ErrorHandler {
    private val log = LoggerFactory.getLogger(javaClass)

    @ExceptionHandler(ApiException::class)
    fun api(e: ApiException): ResponseEntity<ErrorBody> =
        ResponseEntity.status(e.status).body(ErrorBody(ErrorDetail(e.key, e.params)))

    @ExceptionHandler(WebExchangeBindException::class)
    fun bind(e: WebExchangeBindException): ResponseEntity<ErrorBody> {
        val fields = e.fieldErrors.associate { it.field to (it.code ?: "invalid") }
        return validation(fields)
    }

    @ExceptionHandler(ServerWebInputException::class)
    fun input(e: ServerWebInputException): ResponseEntity<ErrorBody> =
        validation(mapOf((e.methodParameter?.parameterName ?: "body") to "invalid"))

    @ExceptionHandler(ResponseStatusException::class)
    fun status(e: ResponseStatusException): ResponseEntity<ErrorBody> = when {
        e.statusCode.value() == 404 ->
            ResponseEntity.status(404).body(ErrorBody(ErrorDetail("NOT_FOUND")))
        e.statusCode.is4xxClientError -> validation(mapOf("request" to "invalid"))
        else -> internal(e)
    }

    @ExceptionHandler(Exception::class)
    fun internal(e: Exception): ResponseEntity<ErrorBody> {
        val requestId = UUID.randomUUID().toString()
        log.error("INTERNAL_ERROR request_id={}", requestId, e)
        return ResponseEntity.status(500).body(ErrorBody(ErrorDetail("INTERNAL_ERROR", mapOf("request_id" to requestId))))
    }

    private fun validation(fields: Map<String, String>) =
        ResponseEntity.status(422).body(ErrorBody(ErrorDetail("VALIDATION_ERROR", mapOf("fields" to fields))))
}
