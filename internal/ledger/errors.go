package ledger

import (
	"errors"
	"fmt"
	"net/http"
)

// Stable error codes. Callers switch on these, never on messages.
const (
	CodeNotFound          = "not_found"
	CodeInvalidTransition = "invalid_transition"
	CodeClaimConflict     = "claim_conflict"
	CodeLeaseExpired      = "lease_expired"
	CodeValidationFailed  = "validation_failed"
	CodeInternal          = "internal_error"
)

// Error is a coded ledger error.
type Error struct {
	err    error
	code   string
	status int
}

func newError(code string, status int, format string, args ...any) *Error {
	return &Error{err: fmt.Errorf(format, args...), code: code, status: status}
}

func (e *Error) Error() string   { return e.err.Error() }
func (e *Error) Unwrap() error   { return e.err }
func (e *Error) Code() string    { return e.code }
func (e *Error) HTTPStatus() int { return e.status }

func notFound(id int64) error {
	return newError(CodeNotFound, http.StatusNotFound, "job %d not found", id)
}

func invalidTransition(format string, args ...any) error {
	return newError(CodeInvalidTransition, http.StatusConflict, format, args...)
}

func claimConflict(format string, args ...any) error {
	return newError(CodeClaimConflict, http.StatusConflict, format, args...)
}

func leaseExpired(id int64) error {
	return newError(CodeLeaseExpired, http.StatusConflict, "lease on job %d expired and the job returned to open; claim it again", id)
}

func validationFailed(err error) error {
	return &Error{err: err, code: CodeValidationFailed, status: http.StatusBadRequest}
}

// CodeOf returns err's ledger code, or internal_error for anything else.
func CodeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.code
	}
	return CodeInternal
}

// StatusOf returns err's HTTP status, or 500 for anything that is not a
// ledger error.
func StatusOf(err error) int {
	var e *Error
	if errors.As(err, &e) {
		return e.status
	}
	return http.StatusInternalServerError
}
