package poly

import (
	"context"
	"sync/atomic"
	"time"
)

// WorkerPool executes a user-supplied function fn across a fixed number of
// worker goroutines. Work is submitted through [Op] instances created by
// [NewOperation]. Multiple operations share the same pool and workers.
//
// The pool lives until ctx is cancelled; after that, workers exit and
// new requests are dropped.
type WorkerPool[ReqType, RespType any] struct {
	ctx context.Context

	// in carries closures from per-operation sender goroutines to workers.
	in chan func()

	fn func(context.Context, ReqType) (RespType, error)
}

// New creates a [WorkerPool] with workersCnt goroutines and returns it.
// Workers start immediately and run until ctx is cancelled.
//
//	wp := poly.New(ctx, fetchURL, 20)
func New[ReqType, RespType any](ctx context.Context, fn func(context.Context, ReqType) (RespType, error), workersCnt int) *WorkerPool[ReqType, RespType] {
	wp := &WorkerPool[ReqType, RespType]{
		ctx: ctx,
		in:  make(chan func(), workersCnt),
		fn:  fn,
	}

	for i := 0; i < workersCnt; i++ {
		go func() {
			for {
				select {
				case fn := <-wp.in:
					fn()
				case <-wp.ctx.Done():
					return
				}
			}
		}()
	}

	return wp
}

// NewOperation creates an independent [Op] bound to the pool.
// The returned end function cancels the operation with [ErrOperationEnded]
// and must be called when the operation is no longer needed.
//
// Each Op gets a dedicated sender goroutine that batches calls to
// [Op.AddRequest] and feeds them into the pool's worker channel.
// This avoids creating a goroutine per request.
//
//	op, end := poly.NewOperation(wp, ctx)
//	defer end()
func NewOperation[ReqType, RespType any](wp *WorkerPool[ReqType, RespType], ctx context.Context) (*Op[ReqType, RespType], func()) {
	opCtx, cancel := context.WithCancelCause(ctx)

	op := &Op[ReqType, RespType]{
		ctx:    opCtx,
		cancel: cancel,

		out:         make(chan RespType),
		r:           new(atomic.Int64),
		tDone:       new(atomic.Int64),
		calcTimeSum: new(atomic.Int64),
	}

	q := newSendQueue[ReqType]()

	op.submit = func(req ReqType) {
		op.r.Add(1)
		if !q.push(req) {
			op.r.Add(-1)
		}
	}

	// Sender goroutine: drains the queue in batches and sends closures
	// to the pool channel. Exits when the pool or operation context is
	// cancelled, decrementing r for every unsent request.
	go func() {
		drain := func() {
			if n := q.close(); n > 0 {
				op.r.Add(-int64(n))
			}
		}

		for {
			select {
			case <-q.wake:
			case <-wp.ctx.Done():
				drain()
				return
			case <-opCtx.Done():
				drain()
				return
			}

			batch := q.take()
			for i, req := range batch {
				select {
				case wp.in <- func() { wp.handle(req, op) }:
				case <-wp.ctx.Done():
					op.r.Add(-int64(len(batch) - i))
					drain()
					return
				case <-opCtx.Done():
					op.r.Add(-int64(len(batch) - i))
					drain()
					return
				}
			}
		}
	}()

	endFunc := func() {
		op.cancel(ErrOperationEnded)
	}

	return op, endFunc
}

// handle processes a single request. It is called inside a worker goroutine.
//
// The function receives a merged context that is cancelled when either the
// pool or the operation context is cancelled (via [context.AfterFunc]).
//
// Pending-request counter (r) ownership:
//   - error / cancellation paths: handle decrements r (no result is sent).
//   - success path: r is decremented by the consumer ([Op.Wait] / [Op.Results])
//     after reading from op.out.
//   - send blocked by cancellation: handle rolls back tDone and decrements r.
func (wp *WorkerPool[ReqType, RespType]) handle(req ReqType, op *Op[ReqType, RespType]) {
	if op.ctx.Err() != nil {
		op.r.Add(-1)
		return
	}

	// Merge pool and operation contexts so fn observes cancellation from either.
	ctx, ctxCancel := context.WithCancelCause(op.ctx)
	stop := context.AfterFunc(wp.ctx, func() { ctxCancel(context.Cause(wp.ctx)) })
	defer stop()
	defer ctxCancel(nil)

	st := time.Now()

	res, err := wp.fn(ctx, req)

	if err != nil {
		op.r.Add(-1)
		op.cancel(err)
		return
	}

	op.calcTimeSum.Add(int64(time.Since(st)))
	op.tDone.Add(1)

	select {
	case op.out <- res:

	case <-op.ctx.Done():
		op.tDone.Add(-1)
		op.r.Add(-1)

	case <-wp.ctx.Done():
		op.tDone.Add(-1)
		op.r.Add(-1)
	}
}
