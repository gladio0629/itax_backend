// Package apperr — yagona xato formati: {"error":{"key":"...","params":{}}}.
package apperr

import "net/http"

type Error struct {
	Status int
	Key    string
	Params map[string]any
}

func (e *Error) Error() string { return e.Key }

func New(status int, key string, params map[string]any) *Error {
	if params == nil {
		params = map[string]any{}
	}
	return &Error{Status: status, Key: key, Params: params}
}

// Forbidden — ichki token yo'q yoki noto'g'ri (tashqi chaqiruvchi).
func Forbidden(reason string) *Error {
	return New(http.StatusForbidden, "FORBIDDEN", map[string]any{"reason": reason})
}

func NotFound() *Error { return New(http.StatusNotFound, "NOT_FOUND", nil) }

func Unauthorized() *Error { return New(http.StatusUnauthorized, "AUTH_TOKEN_EXPIRED", nil) }

func Validation(fields map[string]string) *Error {
	return New(http.StatusUnprocessableEntity, "VALIDATION_ERROR", map[string]any{"fields": fields})
}
