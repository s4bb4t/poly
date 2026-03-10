package poly

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-faster/errors"
)

// ErrOperationEnded is the cause set on an operation's context when
// the end function returned by [NewOperation] is called.
var ErrOperationEnded = errors.New("operation ended")

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
		submit func(ReqType)

		// r is the number of in-flight requests (queued + processing + awaiting
		// consumption). Wait and Results decrement it when they consume a result;
		// error and cancellation paths in handle and the sender goroutine
		// decrement it for requests that will never produce a result.
		r  *atomic.Int64
		mu sync.RWMutex

		// metrics
		tDone       *atomic.Int64 // successful results sent
		calcTimeSum *atomic.Int64 // cumulative processing time (nanoseconds)
	}

	// Metrics holds processing statistics for an [Op].
	Metrics struct {
		// OperationsTotal is the number of requests that completed
		// successfully and whose results were sent to the consumer.
		OperationsTotal int

		// AverageProcessingDuration is the mean wall-clock time of
		// successful fn calls, computed as calcTimeSum / OperationsTotal.
		AverageProcessingDuration time.Duration
	}
)

// AddRequest submits a request for processing by the pool.
// It is safe to call from multiple goroutines.
// Requests submitted after the operation or pool context is cancelled are dropped.
func (o *Op[ReqType, RespType]) AddRequest(req ReqType) {
	o.submit(req)
}

// Wait blocks until all submitted requests have been processed or the
// operation context is cancelled. Results are drained internally (not
// forwarded to the caller). On successful completion it returns the
// accumulated [Metrics]; on cancellation it returns the zero value.
//
// Wait must not be called concurrently with [Op.Results] on the same Op.
func (o *Op[ReqType, RespType]) Wait() (m Metrics) {
	for !o.r.CompareAndSwap(0, 0) {
		select {
		case <-o.out:
			o.r.Add(-1)

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

		for !o.r.CompareAndSwap(0, 0) {
			select {
			case res := <-o.out:
				select {
				case out <- res:
					o.r.Add(-1)

				case <-o.ctx.Done():
					return
				}

			case <-o.ctx.Done():
				return
			}
		}
	}()

	return out
}

// Err returns the error that caused the operation to stop, or nil if
// the operation is still running or completed without error.
// After the end function is called, Err returns [ErrOperationEnded].
func (o *Op[ReqType, RespType]) Err() error {
	o.mu.RLock()
	defer o.mu.RUnlock()

	if o.ctx.Err() != nil {
		return context.Cause(o.ctx)
	}

	return o.ctx.Err()
}

// Metrics returns a snapshot of processing statistics. If resetOnRead is
// true, the counters are atomically reset to zero after reading.
func (o *Op[ReqType, RespType]) Metrics(resetOnRead bool) Metrics {
	if resetOnRead {
		o.mu.Lock()
		defer o.mu.Unlock()
		defer o.tDone.Store(0)
		defer o.calcTimeSum.Store(0)

		return Metrics{
			OperationsTotal:           int(o.tDone.Load()),
			AverageProcessingDuration: time.Duration(o.calcTimeSum.Load() / max(1, o.tDone.Load())),
		}
	}

	o.mu.RLock()
	defer o.mu.RUnlock()

	return Metrics{
		OperationsTotal:           int(o.tDone.Load()),
		AverageProcessingDuration: time.Duration(o.calcTimeSum.Load() / max(1, o.tDone.Load())),
	}
}
