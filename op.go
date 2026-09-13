package poly

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

type (
	// Op is a handle to an independent batch of requests submitted to a
	// [WorkerPool]. It tracks the number of pending requests, collects
	// results, and accumulates processing metrics.
	//
	// An Op is created by [NewOperation] and should not be copied.
	// Use [Op.AddRequest] to submit work, [Op.Wait] or [Op.Results] to
	// consume results, and the end function to release resources.
	Op[ReqType, RespType any] struct {
		ctx    context.Context
		cancel context.CancelCauseFunc

		out    chan RespType
		submit func(ReqType) bool

		// continueOnError is set by [WithContinueOnError]: a failing
		// request is recorded instead of cancelling the operation.
		continueOnError bool

		// r is the number of in-flight requests (queued + processing + awaiting
		// consumption). Wait and Results decrement it when they consume a result;
		// error and cancellation paths in handle and the sender goroutine
		// decrement it for requests that will never produce a result.
		r atomic.Int64

		// progress carries a wake-up (capacity 1, coalescing) for every
		// decrement of r that does not come with a result on out.
		// Without it a consumer parked in Wait/Results would sleep
		// through the last failing request and never notice that the
		// operation had finished.
		progress chan struct{}

		// mu guards failures and makes a metrics read-and-reset atomic
		// with respect to other readers. The counters themselves are
		// atomics and are safe to read without it.
		mu       sync.RWMutex
		failures []*Failure[ReqType]

		// metrics
		tDone       atomic.Int64 // successful results sent
		tFailed     atomic.Int64 // requests that returned an error or panicked
		calcTimeSum atomic.Int64 // cumulative processing time (nanoseconds)
	}

	// Metrics holds processing statistics for an [Op].
	Metrics struct {
		// OperationsTotal is the number of requests that completed
		// successfully and whose results were sent to the consumer.
		OperationsTotal int

		// Failed is the number of requests that returned an error or
		// panicked. In the default fail-fast mode this is at most one;
		// with [WithContinueOnError] it counts every bad request.
		Failed int

		// AverageProcessingDuration is the mean wall-clock time of
		// successful fn calls, computed as calcTimeSum / OperationsTotal.
		AverageProcessingDuration time.Duration
	}
)

// AddRequest submits a request for processing by the pool.
// It is safe to call from multiple goroutines.
//
// It reports whether the request was accepted. A request is refused once
// the operation or the pool context is cancelled; see [Op.Err] for the
// reason.
func (o *Op[ReqType, RespType]) AddRequest(req ReqType) bool {
	return o.submit(req)
}

// Wait blocks until all submitted requests have been processed or the
// operation context is cancelled. Results are drained internally (not
// forwarded to the caller). On successful completion it returns the
// accumulated [Metrics]; on cancellation it returns the zero value.
//
// Wait must not be called concurrently with [Op.Results] on the same Op.
func (o *Op[ReqType, RespType]) Wait() (m Metrics) {
	for o.r.Load() != 0 {
		select {
		case <-o.out:
			o.r.Add(-1)

		case <-o.progress:
			// a request was released without producing a result

		case <-o.ctx.Done():
			return
		}
	}

	return o.Metrics(false)
}

// Results returns a channel that receives each successful result as it
// becomes available. The channel is closed when all requests have been
// consumed or the operation context is cancelled.
//
// Results must not be called concurrently with [Op.Wait] on the same Op.
func (o *Op[ReqType, RespType]) Results() <-chan RespType {
	out := make(chan RespType)

	go func() {
		defer close(out)

		for o.r.Load() != 0 {
			select {
			case res := <-o.out:
				select {
				case out <- res:
					o.r.Add(-1)

				case <-o.ctx.Done():
					return
				}

			case <-o.progress:
				// a request was released without producing a result

			case <-o.ctx.Done():
				return
			}
		}
	}()

	return out
}

// Err returns the error that stopped the operation, or nil if the
// operation is still running or finished without failing.
//
// In the default fail-fast mode the error is a [*Failure] naming the
// request that broke, so errors.Is reaches the original error and
// errors.As reaches the request:
//
//	errors.Is(op.Err(), io.ErrUnexpectedEOF)
//
// With [WithContinueOnError] a bad request never stops the operation, so
// Err stays nil and [Op.Failures] is where the errors are.
//
// After the end function is called, Err returns [ErrOperationEnded].
func (o *Op[ReqType, RespType]) Err() error {
	if o.ctx.Err() == nil {
		return nil
	}

	return context.Cause(o.ctx)
}

// Failures returns every request that failed so far, together with its
// error, in the order the failures were recorded. It returns nil when
// nothing has failed.
//
// The returned slice is a copy and is safe to keep. Unlike the counters
// in [Metrics], the failure list is never reset.
func (o *Op[ReqType, RespType]) Failures() []*Failure[ReqType] {
	o.mu.RLock()
	defer o.mu.RUnlock()

	if len(o.failures) == 0 {
		return nil
	}

	return slices.Clone(o.failures)
}

// fail records a request that the user-supplied function could not
// process, and releases it from the in-flight counter.
//
// In fail-fast mode the operation context is cancelled with the failure
// as cause. The cancel happens before the counter is released: a consumer
// parked in Wait or Results wakes up the moment r reaches zero, and it
// must never see a finished operation whose cause is not set yet.
func (o *Op[ReqType, RespType]) fail(req ReqType, err error) {
	f := &Failure[ReqType]{Request: req, Err: err}

	o.mu.Lock()
	o.failures = append(o.failures, f)
	o.mu.Unlock()

	o.tFailed.Add(1)

	if !o.continueOnError {
		o.cancel(f)
	}

	o.release(1)
}

// release removes n requests from the in-flight counter without
// delivering a result, and wakes a consumer so it can re-check whether
// the operation is finished.
func (o *Op[ReqType, RespType]) release(n int) {
	if n <= 0 {
		return
	}

	o.r.Add(-int64(n))

	select {
	case o.progress <- struct{}{}:
	default:
	}
}

// rollback undoes the metrics recorded for a result that was never
// delivered because the operation or the pool was cancelled mid-send,
// and releases the request from the in-flight counter.
func (o *Op[ReqType, RespType]) rollback(d time.Duration) {
	o.calcTimeSum.Add(-int64(d))
	o.tDone.Add(-1)
	o.release(1)
}

// Metrics returns a snapshot of processing statistics. If resetOnRead is
// true, the counters are reset to zero after reading; the failure list
// returned by [Op.Failures] is left untouched.
func (o *Op[ReqType, RespType]) Metrics(resetOnRead bool) Metrics {
	if resetOnRead {
		o.mu.Lock()
		defer o.mu.Unlock()

		m := o.snapshot()

		o.tDone.Store(0)
		o.tFailed.Store(0)
		o.calcTimeSum.Store(0)

		return m
	}

	o.mu.RLock()
	defer o.mu.RUnlock()

	return o.snapshot()
}

// snapshot reads the metric counters. Callers hold o.mu so that a
// read-and-reset cannot interleave with a plain read.
func (o *Op[ReqType, RespType]) snapshot() Metrics {
	done := o.tDone.Load()

	return Metrics{
		OperationsTotal:           int(done),
		Failed:                    int(o.tFailed.Load()),
		AverageProcessingDuration: time.Duration(o.calcTimeSum.Load() / max(1, done)),
	}
}
