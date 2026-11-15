package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/izgo/trip-order/internal/apperr"
)

type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Key    string         `json:"key"`
	Params map[string]any `json:"params"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError — apperr.Error ni o'z formatida, qolgan hamma narsani INTERNAL_ERROR (tafsilotsiz) qilib yozadi.
func writeError(ctx context.Context, w http.ResponseWriter, log *slog.Logger, err error) {
	var ae *apperr.Error
	if !errors.As(err, &ae) {
		rid := requestID(ctx)
		log.ErrorContext(ctx, "ichki xato", "request_id", rid, "err", err)
		ae = apperr.New(http.StatusInternalServerError, "INTERNAL_ERROR", map[string]any{"request_id": rid})
	}
	if ae.Status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", "Bearer")
	}
	writeJSON(w, ae.Status, errorBody{Error: errorDetail{Key: ae.Key, Params: ae.Params}})
}
