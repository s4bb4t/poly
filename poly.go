package poly

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

const workersCnt = 40

type (
	opReq[ReqType any] struct {
		opKey string
		req   ReqType
	}

	WorkerPool[ReqType, RespType any] struct {
		ctx context.Context

		in  chan opReq[ReqType]
		ops map[string]*Op[RespType]

		fn func(context.Context, ReqType) (RespType, error)

		mu sync.RWMutex
	}
)

func New[ReqType, RespType any](ctx context.Context, fn func(context.Context, ReqType) (RespType, error)) *WorkerPool[ReqType, RespType] {
	wp := &WorkerPool[ReqType, RespType]{
		ctx: ctx,
		in:  make(chan opReq[ReqType], workersCnt),
		ops: make(map[string]*Op[RespType]),
		fn:  fn,
	}

	for i := 0; i < workersCnt; i++ {
		go func() {
			for wp.ctx.Err() == nil {
				select {
				case <-wp.ctx.Done():
					return
				case opReq := <-wp.in:
					wp.handle(opReq.req, wp.ops[opReq.opKey])
				}
			}
		}()
	}

	return wp
}

func (wp *WorkerPool[ReqType, RespType]) NewOperation(ctx context.Context) (*Op[RespType], func()) {
	opCtx, cancel := context.WithCancelCause(ctx)

	op := &Op[RespType]{
		key:    uuid.New().String(),
		ctx:    opCtx,
		cancel: cancel,

		out:         make(chan RespType),
		RWMutex:     new(sync.RWMutex),
		r:           new(atomic.Int64),
		tDone:       new(atomic.Int64),
		calcTimeSum: new(atomic.Int64),
	}

	endFunc := func() {
		wp.mu.Lock()
		defer wp.mu.Unlock()

		op.cancel(ErrOperationEnded)
		close(op.out)
		delete(wp.ops, op.key)
	}

	wp.ops[op.key] = op

	return op, endFunc
}

func (wp *WorkerPool[ReqType, RespType]) AddRequest(req ReqType, o *Op[RespType]) {
	o.r.Add(1)

	go func() {
		select {
		case wp.in <- opReq[ReqType]{
			opKey: o.key,
			req:   req,
		}:

		case <-wp.ctx.Done():
		}
	}()
}

func (wp *WorkerPool[ReqType, RespType]) handle(req ReqType, op *Op[RespType]) {
	if op == nil {
		return
	}

	if op.ctx.Err() != nil {
		return
	}

	st := time.Now()

	res, err := wp.fn(op.ctx, req)
	op.calcTimeSum.Add(int64(time.Since(st)))

	if err != nil {
		op.cancel(err)
		return
	}

	op.tDone.Add(1)
	select {
	case op.out <- res:

	case <-op.ctx.Done():
		op.tDone.Add(-1)

	case <-wp.ctx.Done():
		op.tDone.Add(-1)
	}
}
