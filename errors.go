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

// Failure records a single request that could not be processed, together
// with the reason. It is what [Op.Failures] returns, and — in the default
// fail-fast mode — what [Op.Err] reports, so the caller can always tell
// which input broke, not just that something did:
//
//	var f *poly.Failure[Address]
//	if errors.As(op.Err(), &f) {
//		log.Printf("address %s: %v", f.Request, f.Err)
//	}
type Failure[ReqType any] struct {
	// Request is the request that failed, exactly as submitted.
	Request ReqType

	// Err is the error returned by the user-supplied function, or a
	// [*PanicError] if it panicked.
	Err error
}

// Error implements the error interface.
func (f *Failure[ReqType]) Error() string {
	return fmt.Sprintf("poly: request %v: %v", f.Request, f.Err)
}

// Unwrap returns the underlying error so that errors.Is and errors.As
// see straight through the wrapper.
func (f *Failure[ReqType]) Unwrap() error { return f.Err }
