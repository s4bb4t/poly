package poly

import (
	"errors"
	"fmt"
)

// ErrOperationEnded is the cause set on an operation's context when
// the end function returned by [NewOperation] is called.
var ErrOperationEnded = errors.New("poly: operation ended")

// ErrPanic marks an error produced by a panic inside the user-supplied
// function. Use errors.Is to test for it:
//
//	if errors.Is(op.Err(), poly.ErrPanic) { ... }
var ErrPanic = errors.New("poly: panic in worker function")

// PanicError is the error a worker reports when the user-supplied
// function panics. The panic is contained: the worker goroutine survives
// and the panic is turned into a normal request failure.
//
// PanicError unwraps to [ErrPanic], and to the panic value itself when
// that value is an error.
type PanicError struct {
	// Value is whatever was passed to panic.
	Value any

	// Stack is the stack trace captured at the point of recovery.
	Stack []byte
}

// Error implements the error interface.
func (e *PanicError) Error() string {
	return fmt.Sprintf("poly: panic in worker function: %v", e.Value)
}

// Unwrap reports [ErrPanic] and, when the panic value was itself an
// error, that error as well, so errors.Is matches either one.
func (e *PanicError) Unwrap() []error {
	if err, ok := e.Value.(error); ok {
		return []error{ErrPanic, err}
	}
	return []error{ErrPanic}
}
