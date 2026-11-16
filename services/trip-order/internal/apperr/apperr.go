// Package apperr — yagona xato formati: {"error":{"key":"...","params":{}}}.
package apperr

import "net/http"

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

// Forbidden — ruxsat yo'q; reason (ixtiyoriy) params.reason ga yoziladi.
func Forbidden(reason string, extra map[string]any) *Error {
	p := map[string]any{}
	for k, v := range extra {
		p[k] = v
	}
	if reason != "" {
		p["reason"] = reason
	}
	return New(http.StatusForbidden, "FORBIDDEN", p)
}

// NotOwner — F-10: resurs bor, lekin so'rovchiniki emas (boshqa yo'lovchi buyurtmasi, boshqa haydovchi safari).
func NotOwner() *Error { return Forbidden("not_owner", nil) }

func NotFound() *Error { return New(http.StatusNotFound, "NOT_FOUND", nil) }

func OrderNotFound(id string) *Error {
	return New(http.StatusNotFound, "ORDER_NOT_FOUND", map[string]any{"order_id": id})
}

func OrderAlreadyTaken(id string) *Error {
	return New(http.StatusConflict, "ORDER_ALREADY_TAKEN", map[string]any{"order_id": id})
}

func WalletLimitReached(limit int64) *Error {
	return New(http.StatusPaymentRequired, "WALLET_LIMIT_REACHED", map[string]any{"limit": limit})
}

// Validation — maydon nomi -> xato kaliti (masalan {"phone":"invalid_format"}).
func Validation(fields map[string]string) *Error {
	return New(http.StatusUnprocessableEntity, "VALIDATION_ERROR", map[string]any{"fields": fields})
}

// Narx taklifi (quote) xatolari: buyurtma faqat ko'rsatilgan narx bilan yaratiladi, aks holda aniq rad etiladi.
func QuoteNotFound(id string) *Error {
	return New(http.StatusNotFound, "QUOTE_NOT_FOUND", map[string]any{"quote_id": id})
}

func QuoteExpired(id string) *Error {
	return New(http.StatusConflict, "QUOTE_EXPIRED", map[string]any{"quote_id": id})
}

func QuoteAlreadyUsed(id string) *Error {
	return New(http.StatusConflict, "QUOTE_ALREADY_USED", map[string]any{"quote_id": id})
}

// QuoteMismatch — buyurtma maydonlari (tur, o'rindiqlar, sinf, yuk mashinasi, manzillar) taklifdagidan farq qiladi.
func QuoteMismatch(id string) *Error {
	return New(http.StatusConflict, "QUOTE_MISMATCH", map[string]any{"quote_id": id})
}
