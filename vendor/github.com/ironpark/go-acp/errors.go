package acp

import "fmt"

// RequestError represents a JSON-RPC error with a structured error code.
//
// This type is used to return protocol-level errors from handlers with
// the correct JSON-RPC error code, matching the behavior of the
// TypeScript and Python reference SDKs.
//
// Use the factory functions (ErrParseError, ErrMethodNotFound, etc.)
// to create errors with the correct codes.
type RequestError struct {
	Code    ErrorCode
	Msg     string
	Details any
}

func (e *RequestError) Error() string {
	if e.Details != nil {
		return fmt.Sprintf("JSON-RPC error %d: %s (details: %v)", e.Code, e.Msg, e.Details)
	}
	return fmt.Sprintf("JSON-RPC error %d: %s", e.Code, e.Msg)
}

// ErrParseError creates a parse error (-32700).
func ErrParseError(data any, msg ...string) *RequestError {
	m := "Parse error"
	if len(msg) > 0 {
		m = msg[0]
	}
	return &RequestError{Code: ErrorCodeParseError, Msg: m, Details: data}
}

// ErrInvalidRequest creates an invalid request error (-32600).
func ErrInvalidRequest(data any, msg ...string) *RequestError {
	m := "Invalid request"
	if len(msg) > 0 {
		m = msg[0]
	}
	return &RequestError{Code: ErrorCodeInvalidRequest, Msg: m, Details: data}
}

// ErrMethodNotFound creates a method not found error (-32601).
func ErrMethodNotFound(method string) *RequestError {
	return &RequestError{Code: ErrorCodeMethodNotFound, Msg: fmt.Sprintf("Method not found: %s", method)}
}

// ErrInvalidParams creates an invalid params error (-32602).
func ErrInvalidParams(data any, msg ...string) *RequestError {
	m := "Invalid params"
	if len(msg) > 0 {
		m = msg[0]
	}
	return &RequestError{Code: ErrorCodeInvalidParams, Msg: m, Details: data}
}

// ErrInternalError creates an internal error (-32603).
func ErrInternalError(data any, msg ...string) *RequestError {
	m := "Internal error"
	if len(msg) > 0 {
		m = msg[0]
	}
	return &RequestError{Code: ErrorCodeInternalError, Msg: m, Details: data}
}

// ErrAuthRequired creates an authentication required error (-32000).
func ErrAuthRequired(data any, msg ...string) *RequestError {
	m := "Authentication required"
	if len(msg) > 0 {
		m = msg[0]
	}
	return &RequestError{Code: ErrorCodeAuthenticationRequired, Msg: m, Details: data}
}

// ErrResourceNotFound creates a resource not found error (-32002).
func ErrResourceNotFound(uri ...string) *RequestError {
	m := "Resource not found"
	if len(uri) > 0 {
		m = fmt.Sprintf("Resource not found: %s", uri[0])
	}
	return &RequestError{Code: ErrorCodeResourceNotFound, Msg: m}
}
