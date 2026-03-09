//go:build !race

package poly

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var (
	errTest   = errors.New("test error")
	errCancel = errors.New("operation cancelled")
)

func TestWorkerPool_BasicOperation_Results(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	counter := int64(0)
	wp := New(ctx, func(_ context.Context, req int) (int, error) {
		atomic.AddInt64(&counter, 1)
		return req, nil
	})

	const requests = 100
	for i := 0; i < requests; i++ {
		wp.AddRequest(i)
	}

	for res := range wp.Results() {
		_ = res
	}

	if got, want := atomic.LoadInt64(&counter), int64(requests); got != want {
		t.Errorf("processed %d requests, want %d", got, want)
	}
}

func TestWorkerPool_BasicOperation_Wait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	counter := int64(0)
	wp := New(ctx, func(_ context.Context, req int) (int, error) {
		atomic.AddInt64(&counter, 1)
		return req, nil
	})

	const requests = 100
	for i := 0; i < requests; i++ {
		wp.AddRequest(i)
	}

	fmt.Println(wp.Wait())

	if got, want := atomic.LoadInt64(&counter), int64(requests); got != want {
		t.Errorf("processed %d requests, want %d", got, want)
	}
}

func TestWorkerPool_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	time.Sleep(10 * time.Millisecond) // let workers start
	cancel()

	wp := New(ctx, func(_ context.Context, _ int) (int, error) {
		t.Error("worker should not process requests after ctx cancellation")
		return 0, nil
	})

	wp.AddRequest(1)
	wp.Wait()
}

func TestWorkerPool_OpCtxUpdate(t *testing.T) {
	parentCtx, parentCancel := context.WithCancel(context.Background())
	defer parentCancel()

	fn := func(_ context.Context, req int) (int, error) {
		return req * 2, nil
	}

	wpCtx, wpCancel := context.WithCancel(context.Background())
	wp := New(wpCtx, fn)

	opCtx, opCancel := context.WithCancelCause(parentCtx)
	wp.UpdCtx(opCtx)

	// Cancel opCtx
	opCancel(errCancel)
	time.Sleep(10 * time.Millisecond)

	if got, want := wp.Err(), errCancel; !errors.Is(got, want) {
		t.Errorf("wp.Err() = %v, want %v", got, want)
	}

	wpCancel()
	wp.Wait()
}

func TestWorkerPool_OpCtxErrorPropagation(t *testing.T) {
	parentCtx, parentCancel := context.WithCancel(context.Background())
	defer parentCancel()

	wpCtx, wpCancel := context.WithCancel(context.Background())
	wp := New(wpCtx, func(ctx context.Context, _ int) (int, error) {
		<-ctx.Done()
		return 0, nil
	})

	// Update with context that will timeout
	opCtx, opCancel := context.WithTimeout(parentCtx, 50*time.Millisecond)
	defer opCancel()

	wp.UpdCtx(opCtx)

	const requests = 10
	for i := 0; i < requests; i++ {
		wp.AddRequest(i)
	}

	wp.Wait()
	if !errors.Is(wp.Err(), context.DeadlineExceeded) {
		t.Errorf("wp.Err() did not propagate timeout, got %v", wp.Err())
	}
	wpCancel()
}

func TestWorkerPool_MultipleOpCtxUpdates(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wp := New(ctx, func(ctx context.Context, req int) (int, error) {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(10 * time.Millisecond):
			return req, nil
		}
	})

	// First op context
	ctx1, cancel1 := context.WithTimeout(ctx, 100*time.Millisecond)
	wp.UpdCtx(ctx1)
	for i := 0; i < 5; i++ {
		wp.AddRequest(i)
	}
	cancel1()

	time.Sleep(20 * time.Millisecond)

	// Second op context
	ctx2, cancel2 := context.WithTimeout(ctx, 100*time.Millisecond)
	wp.UpdCtx(ctx2)
	for i := 5; i < 10; i++ {
		wp.AddRequest(i)
	}
	cancel2()

	wp.Wait()
}

func TestWorkerPool_OutputChannelBlock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	processed := int64(0)
	wp := New(ctx, func(_ context.Context, req int) (int, error) {
		atomic.AddInt64(&processed, 1)
		return req, nil
	})

	// Fill output buffer
	for i := 0; i < workersCnt; i++ {
		wp.AddRequest(i)
	}

	// Wait a bit for workers to block on full output channel
	time.Sleep(50 * time.Millisecond)

	if atomic.LoadInt64(&processed) < int64(workersCnt/2) {
		t.Error("expected workers to process requests even when output is blocked")
	}
}

func TestWorkerPool_ErrorFromWorker(t *testing.T) {
	parentCtx, parentCancel := context.WithCancel(context.Background())
	defer parentCancel()

	wpCtx, wpCancel := context.WithCancel(parentCtx)
	wp := New(wpCtx, func(_ context.Context, req int) (int, error) {
		if req == 5 {
			return 0, errTest
		}
		return req, nil
	})

	for i := 0; i < 10; i++ {
		wp.AddRequest(i)
	}

	wp.Wait()
	if !errors.Is(wp.Err(), errTest) {
		t.Errorf("wp.Err() = %v, want %v", wp.Err(), errTest)
	}
	wpCancel()
}

func TestWorkerPool_ConcurrentAccess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wp := New(ctx, func(_ context.Context, req int) (int, error) {
		time.Sleep(time.Millisecond)
		return req, nil
	})

	const concurrentReqs = 1000
	var wg sync.WaitGroup
	wg.Add(concurrentReqs)

	for i := 0; i < concurrentReqs; i++ {
		go func(id int) {
			defer wg.Done()
			wp.AddRequest(id)
		}(i)
	}

	go func() {
		time.Sleep(100 * time.Millisecond)
		wp.UpdCtx(context.Background())
	}()

	wg.Wait()
	wp.Wait()
}

func TestWorkerPool_OpCtxNilAfterError(t *testing.T) {
	parentCtx, parentCancel := context.WithCancel(context.Background())
	defer parentCancel()

	wpCtx, wpCancel := context.WithCancel(context.Background())
	wp := New(wpCtx, func(_ context.Context, _ int) (int, error) {
		return 0, errTest
	})

	opCtx, cancel := context.WithCancel(parentCtx)
	defer cancel()

	wp.UpdCtx(opCtx)

	wp.AddRequest(1)
	wp.Wait()

	if wp.opCtx.Err() == nil {
		t.Error("opCtx.Err() should have returned an error")
	}

	wpErr := wp.Err()
	if wpErr == nil {
		t.Error("wp.Err() should have returned an error")
	}

	if !errors.Is(wpErr, errTest) {
		t.Errorf("wp.Err() = %v, want %v", wpErr, errTest)
	}

	if wp.opCtx.Err() != nil {
		t.Error("opCtx err should be nil after error")
	}
	wpCancel()
}

func TestWorkerPool_OpCtxRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wp := New(ctx, func(opCtx context.Context, _ int) (int, error) {
		if opCtx == nil {
			return 0, errors.New("opCtx is nil")
		}
		return 42, nil
	})

	opCtx, opCancel := context.WithCancel(ctx)
	defer opCancel()
	wp.UpdCtx(opCtx)

	wp.AddRequest(1)
	wp.Wait()
}
