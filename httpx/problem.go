package httpx

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
)

// Error is an API error that is safe to show to the client. It is written as
// application/problem+json (RFC 9457). Any other error becomes a generic 500.
type Error struct {
	Status int          `json:"status"`
	Type   string       `json:"type"`
	Title  string       `json:"title"`
	Detail string       `json:"detail,omitempty"`
	Errors []FieldError `json:"errors,omitempty"`
}

// FieldError points at one invalid input field.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (e *Error) Error() string {
	if e.Detail != "" {
		return e.Title + ": " + e.Detail
	}
	return e.Title
}

// Problem type URIs are relative references, resolved against the app's URL.
const typePrefix = "/problems/"

// Common errors. Handlers return them, or build their own with NewError.
var (
	ErrNotFound     = NewError(http.StatusNotFound, "not-found", "Not found")
	ErrUnauthorized = NewError(http.StatusUnauthorized, "unauthorized", "Sign in to continue")
	ErrForbidden    = NewError(http.StatusForbidden, "forbidden", "You do not have permission to do this")
	ErrCSRF         = NewError(http.StatusForbidden, "csrf", "Missing or invalid CSRF token; reload the page and try again")
	ErrTooMany      = NewError(http.StatusTooManyRequests, "rate-limited", "Too many attempts; wait a few minutes and try again")
	ErrTooLarge     = NewError(http.StatusRequestEntityTooLarge, "too-large", "Request body is too large")
)

// NewError builds an Error with the problem type /problems/<slug>.
func NewError(status int, slug, title string) *Error {
	return &Error{Status: status, Type: typePrefix + slug, Title: title}
}

// WithDetail returns a copy of e with a detail message.
func (e *Error) WithDetail(detail string) *Error {
	c := *e
	c.Detail = detail
	return &c
}

// Invalid returns a 422 validation error listing the bad fields.
func Invalid(fields ...FieldError) *Error {
	e := NewError(http.StatusUnprocessableEntity, "validation", "Some fields are invalid")
	e.Errors = fields
	return e
}

// BadRequest returns a 400 error with a detail message.
func BadRequest(detail string) *Error {
	return NewError(http.StatusBadRequest, "bad-request", "The request is malformed").WithDetail(detail)
}

// Conflict returns a 409 error with a detail message.
func Conflict(detail string) *Error {
	return NewError(http.StatusConflict, "conflict", "This conflicts with existing data").WithDetail(detail)
}

// IsStatus reports whether err is an *Error with the given status.
func IsStatus(err error, status int) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == status
}

// WriteError writes err as problem+json. Errors that are not *Error are logged with the request ID
// and answered with a generic 500, so internals never reach the client.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	var e *Error
	if !errors.As(err, &e) {
		if errors.Is(err, context.Canceled) {
			return // the client went away
		}
		Logger(r.Context()).Error("request failed", slog.String("error", err.Error()))
		e = NewError(http.StatusInternalServerError, "internal", "Something went wrong on our side")
		e.Detail = "Request ID " + RequestID(r.Context())
	}
	if e.Status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", `Session realm="app"`)
	}
	writeJSON(w, e.Status, "application/problem+json", e)
}
