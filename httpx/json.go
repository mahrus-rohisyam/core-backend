package httpx

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// DefaultBodyLimit is the largest JSON body Decode accepts unless a route sets another limit.
const DefaultBodyLimit = 64 << 10

// Decode reads one JSON object from the request body into dst. Unknown fields, trailing data,
// a missing or wrong content type and bodies over DefaultBodyLimit are rejected with a client
// error. Requiring application/json also blocks cross-site HTML form posts, which cannot set it.
func Decode(r *http.Request, dst any) error {
	return DecodeLimit(r, dst, DefaultBodyLimit)
}

// DecodeLimit is Decode with a custom size limit in bytes.
func DecodeLimit(r *http.Request, dst any, limit int64) error {
	ct := r.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		return NewError(http.StatusUnsupportedMediaType, "unsupported-media-type", "Send the body as application/json")
	}
	body := http.MaxBytesReader(nil, r.Body, limit)
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		var syntax *json.SyntaxError
		var typ *json.UnmarshalTypeError
		switch {
		case errors.As(err, &tooLarge):
			return ErrTooLarge
		case errors.Is(err, io.EOF):
			return BadRequest("the body is empty")
		case errors.As(err, &syntax), errors.Is(err, io.ErrUnexpectedEOF):
			return BadRequest("the body is not valid JSON")
		case errors.As(err, &typ):
			return Invalid(FieldError{Field: typ.Field, Message: fmt.Sprintf("must be a %s", typ.Type)})
		case strings.HasPrefix(err.Error(), "json: unknown field "):
			return BadRequest(strings.TrimPrefix(err.Error(), "json: "))
		default:
			return BadRequest("the body could not be read")
		}
	}
	if dec.More() {
		return BadRequest("the body must contain a single JSON object")
	}
	return nil
}

// JSON writes v as JSON with the given status.
func JSON(w http.ResponseWriter, status int, v any) {
	writeJSON(w, status, "application/json", v)
}

// NoContent writes 204.
func NoContent(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) }

func writeJSON(w http.ResponseWriter, status int, contentType string, v any) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(true)
	if err := enc.Encode(v); err != nil {
		http.Error(w, `{"title":"response could not be encoded"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", contentType+"; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}
