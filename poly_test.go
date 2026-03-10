//go:build !race

package poly

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
)

var (
	errTest   = errors.New("test error")
	errCancel = errors.New("operation cancelled")
)

var counter atomic.Int64
var ctx = context.Background()
var wp = New(ctx, func(_ context.Context, req int) (int, error) {
	counter.Add(1)
	return req, nil
})

func TestWorkerPool_BasicOperation_Results(t *testing.T) {
	op, cancel := wp.NewOperation(context.Background())
	defer cancel()

	const requests = 100
	for i := 0; i < requests; i++ {
		wp.AddRequest(i, op)
	}

	cnt := 0
	for res := range op.Results() {
		_ = res
		cnt++
	}

	if got, want := cnt, requests; got != want {
		t.Errorf("processed %d requests, want %d", got, want)
	}

	if op.Metrics(false).OperationsTotal != requests {
		t.Errorf("processed %d requests, want %d", op.Metrics(false).OperationsTotal, requests)
	}
}

func TestWorkerPool_BasicOperation_Wait(t *testing.T) {
	op, cancel := wp.NewOperation(context.Background())
	defer cancel()

	counter.Store(0)

	const requests = 100
	for i := 0; i < requests; i++ {
		wp.AddRequest(i, op)
	}

	m := op.Wait()
	fmt.Println(m)

	if got, want := counter.Load(), int64(requests); got != want || m.OperationsTotal != requests {
		t.Errorf("processed %d requests, want %d", got, want)
	}
}
