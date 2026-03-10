package poly

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-faster/errors"
)

var ErrOperationEnded = errors.New("operation ended")

type (
	Op[RespType any] struct {
		key string

		ctx    context.Context
		cancel context.CancelCauseFunc

		out chan RespType

		r *atomic.Int64
		*sync.RWMutex

		// metrics
		tDone       *atomic.Int64
		calcTimeSum *atomic.Int64
	}

	Metrics struct {
		OperationsTotal           int
		AverageProcessingDuration time.Duration
	}
)

func (o *Op[RespType]) Wait() (m Metrics) {
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

func (o *Op[RespType]) Results() <-chan RespType {
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

func (o *Op[RespType]) Err() error {
	o.RLock()
	defer o.RUnlock()

	if o.ctx.Err() != nil {
		return context.Cause(o.ctx)
	}

	return o.ctx.Err()
}

func (o *Op[RespType]) Metrics(resetOnRead bool) Metrics {
	if resetOnRead {
		o.Lock()
		defer o.Unlock()
		defer o.tDone.Store(0)
		defer o.calcTimeSum.Store(0)

		return Metrics{
			OperationsTotal:           int(o.tDone.Load()),
			AverageProcessingDuration: time.Duration(o.calcTimeSum.Load() / max(1, o.tDone.Load())),
		}
	}

	o.RLock()
	defer o.RUnlock()

	return Metrics{
		OperationsTotal:           int(o.tDone.Load()),
		AverageProcessingDuration: time.Duration(o.calcTimeSum.Load() / max(1, o.tDone.Load())),
	}
}
