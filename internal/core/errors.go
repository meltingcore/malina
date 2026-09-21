package core

import (
	"errors"
	"fmt"
)

// Error is a stable machine-readable error returned by the core and desktop APIs.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Cause   error  `json:"-"`
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func (e *Error) Unwrap() error { return e.Cause }

// NewError creates an error without an underlying cause.
func NewError(code, message string) *Error {
	return &Error{Code: code, Message: message}
}

// WrapError creates a stable error while preserving its underlying cause.
func WrapError(code, message string, cause error) *Error {
	return &Error{Code: code, Message: message, Cause: cause}
}

// ErrorPayload converts an error into the JSON shape used by the CLI.
func ErrorPayload(err error) map[string]string {
	var typed *Error
	if errors.As(err, &typed) {
		return map[string]string{"code": typed.Code, "message": typed.Message}
	}
	return map[string]string{"code": "UNEXPECTED_ERROR", "message": fmt.Sprint(err)}
}
