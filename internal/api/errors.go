package api

import (
	"errors"
	"fmt"
)

// ErrorKind classifies failures so the UI can explain them and suggest a fix.
type ErrorKind int

const (
	KindServer ErrorKind = iota
	KindUnreachable
	KindUnauthorized
	KindNotFound
	KindConflict
	KindUnsupported
	KindInvalid
)

func (k ErrorKind) String() string {
	switch k {
	case KindUnreachable:
		return "unreachable"
	case KindUnauthorized:
		return "unauthorized"
	case KindNotFound:
		return "not found"
	case KindConflict:
		return "conflict"
	case KindUnsupported:
		return "unsupported"
	case KindInvalid:
		return "invalid"
	default:
		return "server error"
	}
}

// Error is the only error type a Backend returns.
type Error struct {
	Kind    ErrorKind
	Message string
	Hint    string // what the user can try next
	Status  int    // HTTP status or CLI exit code, when known
	Line    int    // for Invalid policy errors, 1-based; 0 if unknown
	Col     int
	Err     error // wrapped cause
}

func (e *Error) Error() string {
	if e.Message == "" {
		return e.Kind.String()
	}
	return e.Kind.String() + ": " + e.Message
}

func (e *Error) Unwrap() error { return e.Err }

// Errf builds an *Error with a formatted message.
func Errf(kind ErrorKind, format string, args ...any) *Error {
	return &Error{Kind: kind, Message: fmt.Sprintf(format, args...), Hint: defaultHint(kind)}
}

// AsError extracts an *Error, wrapping foreign errors as KindServer.
func AsError(err error) *Error {
	if err == nil {
		return nil
	}
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return &Error{Kind: KindServer, Message: err.Error(), Err: err}
}

// IsKind reports whether err is an *Error of the given kind.
func IsKind(err error, kind ErrorKind) bool {
	var e *Error
	return errors.As(err, &e) && e.Kind == kind
}

func defaultHint(kind ErrorKind) string {
	switch kind {
	case KindUnreachable:
		return "Check the server address and that headscale is running."
	case KindUnauthorized:
		return "The API key was rejected. Create one with `headscale apikeys create`."
	case KindUnsupported:
		return "This server version does not support that operation."
	case KindNotFound:
		return "It may have been deleted. Refresh and try again."
	default:
		return ""
	}
}
