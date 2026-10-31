package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
)

// Success responses are {"data": ...}; errors are {"error": {"code", "message"}}.
type dataEnvelope struct {
	Data any `json:"data"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type errorEnvelope struct {
	Error apiError `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("encode response", "err", err)
	}
}

func writeData(w http.ResponseWriter, status int, data any) {
	writeJSON(w, status, dataEnvelope{Data: data})
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorEnvelope{Error: apiError{Code: code, Message: message}})
}

// decodeJSON reads a JSON body into dst. Requiring application/json also
// blocks simple cross-site form posts (a CSRF defence alongside SameSite).
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if mt := r.Header.Get("Content-Type"); mt != "application/json" && !strings.HasPrefix(mt, "application/json;") {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must be a valid JSON object with known fields")
		return false
	}
	return true
}

// writeInternal logs the real error and returns a generic message so internal
// details (SQL, credentials) never reach the client.
func writeInternal(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("internal error", "method", r.Method, "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, "internal_error", "an internal error occurred")
}
