package poly

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

const workersCnt = 40

type (
	WorkerPool[ReqType, RespType any] struct {
		ctx      context.Context
		opCtx    context.Context
		opCancel context.CancelCauseFunc

		in  chan ReqType
		out chan RespType

		metrics metrics

		opCounter atomic.Int64
		mu        sync.RWMutex
	}

	metrics struct {
		opDone      atomic.Int64
		calcTimeSum atomic.Int64
	}

	Metrics struct {
		OperationsTotal           int
		AverageProcessingDuration time.Duration
	}
)

func New[ReqType, RespType any](ctx context.Context, fn func(context.Context, ReqType) (RespType, error)) *WorkerPool[ReqType, RespType] {
	opCtx, opCancel := context.WithCancelCause(ctx)

	wp := &WorkerPool[ReqType, RespType]{
		ctx:      ctx,
		opCtx:    opCtx,
		opCancel: opCancel,
		in:       make(chan ReqType, workersCnt),
		out:      make(chan RespType, workersCnt),
	}

	for i := 0; i < workersCnt; i++ {
		go func() {
			for wp.ctx.Err() == nil {
				func() {
					defer wp.opCounter.Add(-1)
					defer wp.metrics.opDone.Add(1)

					select {
					case <-wp.ctx.Done():
						return
					case req := <-wp.in:
						st := time.Now()

						res, err := fn(wp.opCtx, req)
						wp.metrics.calcTimeSum.Add(int64(time.Since(st)))

						if err != nil {
							wp.opCancel(err)
							return
						}

						select {
						case wp.out <- res:
						case <-wp.opCtx.Done():
						case <-wp.ctx.Done():
						}
					}
				}()
			}
		}()
	}

	return wp
}

func (wp *WorkerPool[ReqType, RespType]) Metrics() (m Metrics) {
	m = Metrics{
		OperationsTotal:           int(wp.metrics.opDone.Load()),
		AverageProcessingDuration: time.Duration(wp.metrics.calcTimeSum.Load() / max(1, wp.metrics.opDone.Load())),
	}

	wp.metrics.opDone.Store(0)
	wp.metrics.calcTimeSum.Store(0)

	return m
}

func (wp *WorkerPool[ReqType, RespType]) Wait() (m Metrics) {
	defer func() {
		m = Metrics{
			OperationsTotal:           int(wp.metrics.opDone.Load()),
			AverageProcessingDuration: time.Duration(wp.metrics.calcTimeSum.Load() / max(1, wp.metrics.opDone.Load())),
		}

		wp.metrics.opDone.Store(0)
		wp.metrics.calcTimeSum.Store(0)
	}()

	for !wp.opCounter.CompareAndSwap(0, 0) {
		select {
		case <-wp.ctx.Done():
			return

		case <-wp.opCtx.Done():
			return

		case <-wp.out:
		}
	}

	return m
}

func (wp *WorkerPool[ReqType, RespType]) Results() <-chan RespType {
	out := make(chan RespType)

	go func() {
		defer close(out)

		for !wp.opCounter.CompareAndSwap(0, 0) {
			select {
			case <-wp.ctx.Done():
				return
			case out <- <-wp.out:
			}
		}
	}()

	return out
}

func (wp *WorkerPool[ReqType, RespType]) AddRequest(req ReqType) {
	wp.opCounter.Add(1)
	go func() {
		select {
		case <-wp.ctx.Done():
		case wp.in <- req:
		}
	}()

}

func (wp *WorkerPool[ReqType, RespType]) UpdCtx(ctx context.Context) {
	wp.mu.Lock()
	defer wp.mu.Unlock()

	ctx, cancel := context.WithCancelCause(ctx)

	wp.opCtx = ctx
	wp.opCancel = cancel
}

func (wp *WorkerPool[ReqType, RespType]) OpCtx() context.Context {
	wp.mu.RLock()
	defer wp.mu.RUnlock()

	return wp.opCtx
}

func (wp *WorkerPool[ReqType, RespType]) Err() error {
	wp.mu.Lock()
	defer wp.mu.Unlock()

	if wp.opCtx.Err() != nil {
		defer func() {
			wp.opCtx = context.Background()
		}()

		return context.Cause(wp.opCtx)
	}

	return nil
}
