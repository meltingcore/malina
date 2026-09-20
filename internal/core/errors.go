package core

import "fmt"

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

func NewError(code, message string) *Error {
	return &Error{Code: code, Message: message}
}

func WrapError(code, message string, cause error) *Error {
	return &Error{Code: code, Message: message, Cause: cause}
}

func ErrorPayload(err error) map[string]string {
	if typed, ok := err.(*Error); ok {
		return map[string]string{"code": typed.Code, "message": typed.Message}
	}
	return map[string]string{"code": "UNEXPECTED_ERROR", "message": fmt.Sprint(err)}
}
