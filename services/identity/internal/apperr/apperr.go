// Package apperr — yagona xato formati: {"error":{"key":"...","params":{}}}.
package apperr

import (
	"math"
	"net/http"
	"time"
)

// Error — klientga qaytariladigan xato (HTTP status + kalit + parametrlar).
type Error struct {
	Status int
	Key    string
	Params map[string]any
}

func (e *Error) Error() string { return e.Key }

// New — yangi xato yaratadi; params nil bo'lsa bo'sh obyekt ({}) bo'ladi.
func New(status int, key string, params map[string]any) *Error {
	if params == nil {
		params = map[string]any{}
	}
	return &Error{Status: status, Key: key, Params: params}
}

// Unauthorized — token yo'q, noto'g'ri, muddati o'tgan yoki bekor qilingan.
func Unauthorized() *Error { return New(http.StatusUnauthorized, "AUTH_TOKEN_EXPIRED", nil) }

func OTPExpired() *Error { return New(http.StatusUnauthorized, "AUTH_OTP_EXPIRED", nil) }

func OTPInvalid(attemptsLeft int) *Error {
	return New(http.StatusUnauthorized, "AUTH_OTP_INVALID", map[string]any{"attempts_left": attemptsLeft})
}

// RateLimited — OTP so'rovlari limiti (F-04). Kalit shared/errors/errors.json ga qo'shilgan.
func RateLimited(retryAfter time.Duration) *Error {
	secs := int(math.Ceil(retryAfter.Seconds()))
	if secs < 1 {
		secs = 1
	}
	return New(http.StatusTooManyRequests, "AUTH_OTP_RATE_LIMITED", map[string]any{"retry_after": secs})
}

func Forbidden() *Error { return New(http.StatusForbidden, "FORBIDDEN", nil) }

// ForbiddenReason — FORBIDDEN + params.reason (katalog: shared/errors/errors.json → FORBIDDEN.reasons).
func ForbiddenReason(reason string) *Error {
	return New(http.StatusForbidden, "FORBIDDEN", map[string]any{"reason": reason})
}

func NotFound() *Error { return New(http.StatusNotFound, "NOT_FOUND", nil) }

// StaffEmailTaken / StaffPhoneTaken — xodim yaratishda unikal maydon band (shared/errors/errors.json).
func StaffEmailTaken() *Error { return New(http.StatusConflict, "STAFF_EMAIL_TAKEN", nil) }

func StaffPhoneTaken() *Error { return New(http.StatusConflict, "STAFF_PHONE_TAKEN", nil) }

func StaffNotFound(id string) *Error {
	return New(http.StatusNotFound, "STAFF_NOT_FOUND", map[string]any{"staff_id": id})
}

// Validation — maydon nomi -> xato kaliti (masalan {"phone":"invalid_format"}).
func Validation(fields map[string]string) *Error {
	return New(http.StatusUnprocessableEntity, "VALIDATION_ERROR", map[string]any{"fields": fields})
}
