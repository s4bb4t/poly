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

		// closed is set by [Op.Done] and means "no further requests will
		// be submitted". Together with r == 0 it is what makes an
		// operation finished; noMore wakes consumers when it flips.
		closed atomic.Bool
		noMore chan struct{}

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
// It reports whether the request was accepted. A request is refused
// after [Op.Done], once the operation or the pool context is cancelled
// (see [Op.Err] for the reason), or when the queue is full and the
// operation was created with [WithRejectOnFull].
//
// By default the queue is unbounded and AddRequest never blocks; with
// [WithMaxQueue] it applies backpressure and blocks while the queue is
// full.
func (o *Op[ReqType, RespType]) AddRequest(req ReqType) bool {
	return o.submit(req)
}

// Done declares that no further requests will be submitted. It is what
// lets [Op.Wait] and [Op.Results] tell "everything is processed" apart
// from "nothing has been submitted yet", so it must be called on every
// operation that is waited on:
//
//	for _, req := range reqs {
//		op.AddRequest(req)
//	}
//	op.Done()
//
//	for res := range op.Results() { ... }
//
// Done is idempotent and safe to call from any goroutine, but it must
// not race with [Op.AddRequest]: every AddRequest has to return before
// Done is called, exactly as with [sync.WaitGroup.Add] and Wait. Later
// requests are refused.
//
// If requests are produced from the same goroutine that consumes
// results, call Done from the producer once it is finished — otherwise
// the consumer waits for requests that will never arrive.
func (o *Op[ReqType, RespType]) Done() {
	if o.closed.CompareAndSwap(false, true) {
		close(o.noMore)
	}
}

// finished reports whether the operation has nothing left to do: the
// caller promised no more requests and every request in flight has been
// accounted for.
//
// The two loads must happen in this order. Reading closed first
// guarantees that every AddRequest that happened before Done has already
// incremented r, so a subsequent r == 0 really does mean "all released".
func (o *Op[ReqType, RespType]) finished() bool {
	return o.closed.Load() && o.r.Load() == 0
}

// pending returns the channel that Done closes, or nil once it is
// already closed — a nil channel blocks forever in a select, which is
// exactly what a consumer that is only waiting for results wants.
func (o *Op[ReqType, RespType]) pending() <-chan struct{} {
	if o.closed.Load() {
		return nil
	}

	return o.noMore
}

// Wait blocks until [Op.Done] has been called and every submitted
// request has been processed, or until the operation context is
// cancelled. Results are drained internally (not forwarded to the
// caller). On successful completion it returns the accumulated
// [Metrics]; on cancellation it returns the zero value.
//
// Wait blocks forever if Done is never called.
//
// Wait must not be called concurrently with [Op.Results] on the same Op.
func (o *Op[ReqType, RespType]) Wait() (m Metrics) {
	for !o.finished() {
		select {
		case <-o.out:
			o.r.Add(-1)

		case <-o.progress:
			// a request was released without producing a result

		case <-o.pending():
			// Done was called; re-check

		case <-o.ctx.Done():
			return
		}
	}

	return o.Metrics(false)
}

// Results returns a channel that receives each successful result as it
// becomes available. The channel is closed once [Op.Done] has been
// called and every submitted request has been consumed, or when the
// operation context is cancelled.
//
// The channel never closes if Done is never called. Results may safely
// be called before the first [Op.AddRequest].
//
// Results must not be called concurrently with [Op.Wait] on the same Op.
func (o *Op[ReqType, RespType]) Results() <-chan RespType {
	out := make(chan RespType)

	go func() {
		defer close(out)

		for !o.finished() {
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

			case <-o.pending():
				// Done was called; re-check

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
