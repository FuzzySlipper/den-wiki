package registry

import "errors"

// Stable error codes for every surface (MCP, CLI). Backend codes such as the
// ledger's pass through unchanged.
const (
	CodeUnknownOperation   = "unknown_operation"
	CodeUnknownField       = "unknown_field"
	CodeInvalidArguments   = "invalid_arguments"
	CodeMissingAgent       = "missing_agent"
	CodeNotFound           = "not_found"
	CodeConflict           = "conflict"
	CodeBackendUnavailable = "backend_unavailable"
	CodeBackendError       = "backend_error"
	CodeInternal           = "internal_error"
)

// Error is a coded error any operation may return.
type Error struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

func (e *Error) Error() string { return e.Message }

// Coded is implemented by backend errors that carry a stable code.
type Coded interface {
	error
	Code() string
}

// ErrorOf turns any error into a coded registry error.
func ErrorOf(err error) *Error {
	var re *Error
	if errors.As(err, &re) {
		return re
	}
	var c Coded
	if errors.As(err, &c) {
		return &Error{Code: c.Code(), Message: c.Error()}
	}
	return &Error{Code: CodeInternal, Message: err.Error()}
}

// Class groups codes for exit statuses and outcome reporting.
type Class string

const (
	ClassUsage       Class = "usage"
	ClassNotFound    Class = "not_found"
	ClassConflict    Class = "conflict"
	ClassUnavailable Class = "unavailable"
	ClassInternal    Class = "internal"
)

// ClassOf classifies an error code.
func ClassOf(code string) Class {
	switch code {
	case CodeUnknownOperation, CodeUnknownField, CodeInvalidArguments, CodeMissingAgent, "validation_failed":
		return ClassUsage
	case CodeNotFound:
		return ClassNotFound
	case CodeConflict, "claim_conflict", "lease_expired", "invalid_transition", "stale_write":
		return ClassConflict
	case CodeBackendUnavailable:
		return ClassUnavailable
	}
	return ClassInternal
}
