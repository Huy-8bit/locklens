package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

type apiError struct {
	Code      string            `json:"code"`
	Message   string            `json:"message"`
	Fields    map[string]string `json:"fields,omitempty"`
	RequestID string            `json:"request_id"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	_ = enc.Encode(v)
}

// writeFieldErrors answers 422 with one message per invalid field.
func writeFieldErrors(w http.ResponseWriter, r *http.Request, fields map[string]string) {
	writeJSON(w, http.StatusUnprocessableEntity, map[string]apiError{"error": {Code: "validation", Message: "invalid input", Fields: fields, RequestID: requestID(r)}})
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, msg string) {
	writeJSON(w, status, map[string]apiError{"error": {Code: code, Message: msg, RequestID: requestID(r)}})
}

// internal logs an unexpected error and returns a generic message, so
// internal details (which could include data from the store) never reach
// clients.
func (s *Server) internal(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, context.Canceled) {
		return
	}
	s.d.Log.Error("request failed", "request_id", requestID(r), "path", r.URL.Path, "error", err.Error())
	writeError(w, r, http.StatusInternalServerError, "internal", "internal error (see server logs for request "+requestID(r)+")")
}

// decode reads a JSON body with a size limit and rejects unknown fields.
func decode(w http.ResponseWriter, r *http.Request, dst any, limit int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return errors.New("request body too large")
		}
		return errors.New("invalid JSON body: " + err.Error())
	}
	return nil
}
