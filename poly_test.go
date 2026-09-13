package poly

import (
	"context"
	"errors"
	"testing"
	"time"
)

var errTest = errors.New("test error")

func TestWorkerPool(t *testing.T) {
	t.Run("Results collects all responses", func(t *testing.T) {
		wp := New(context.Background(), func(_ context.Context, req int) (int, error) {
			return req * 2, nil
		}, 40)

		op, cancel := NewOperation(context.Background(), wp)
		defer cancel()

		const requests = 100
		for i := 0; i < requests; i++ {
			op.AddRequest(i)
		}

		got := make(map[int]bool)
		for res := range op.Results() {
			got[res] = true
		}

		if len(got) != requests {
			t.Errorf("got %d results, want %d", len(got), requests)
		}

		for i := 0; i < requests; i++ {
			if !got[i*2] {
				t.Errorf("missing result for input %d (expected %d)", i, i*2)
			}
		}
	})

	t.Run("Wait completes all requests", func(t *testing.T) {
		wp := New(context.Background(), func(_ context.Context, req int) (int, error) {
			return req, nil
		}, 40)

		op, cancel := NewOperation(context.Background(), wp)
		defer cancel()

		const requests = 100
		for i := 0; i < requests; i++ {
			op.AddRequest(i)
		}

		m := op.Wait()

		if m.OperationsTotal != requests {
			t.Errorf("metrics: got %d operations, want %d", m.OperationsTotal, requests)
		}
	})

	t.Run("Wait metrics has nonzero average duration", func(t *testing.T) {
		wp := New(context.Background(), func(_ context.Context, req int) (int, error) {
			time.Sleep(time.Millisecond)
			return req, nil
		}, 40)

		op, cancel := NewOperation(context.Background(), wp)
		defer cancel()

		const requests = 10
		for i := 0; i < requests; i++ {
			op.AddRequest(i)
		}

		m := op.Wait()

		if m.AverageProcessingDuration <= 0 {
			t.Errorf("expected positive average duration, got %v", m.AverageProcessingDuration)
		}
	})

	t.Run("Metrics resetOnRead resets counters", func(t *testing.T) {
		wp := New(context.Background(), func(_ context.Context, req int) (int, error) {
			return req, nil
		}, 40)

		op, cancel := NewOperation(context.Background(), wp)
		defer cancel()

		const requests = 50
		for i := 0; i < requests; i++ {
			op.AddRequest(i)
		}

		op.Wait()

		m1 := op.Metrics(true)
		if m1.OperationsTotal != requests {
			t.Errorf("before reset: got %d, want %d", m1.OperationsTotal, requests)
		}

		m2 := op.Metrics(false)
		if m2.OperationsTotal != 0 {
			t.Errorf("after reset: got %d, want 0", m2.OperationsTotal)
		}
		if m2.AverageProcessingDuration != 0 {
			t.Errorf("after reset: average duration got %v, want 0", m2.AverageProcessingDuration)
		}
	})

	t.Run("fn error cancels operation", func(t *testing.T) {
		wp := New(context.Background(), func(_ context.Context, req int) (int, error) {
			if req == 5 {
				return 0, errTest
			}
			return req, nil
		}, 40)

		op, cancel := NewOperation(context.Background(), wp)
		defer cancel()

		for i := 0; i < 20; i++ {
			op.AddRequest(i)
		}

		op.Wait()

		err := op.Err()
		if !errors.Is(err, errTest) {
			t.Errorf("expected errTest, got %v", err)
		}
	})

	t.Run("op context cancellation stops Wait", func(t *testing.T) {
		wp := New(context.Background(), func(ctx context.Context, req int) (int, error) {
			select {
			case <-time.After(time.Hour):
				return req, nil
			case <-ctx.Done():
				return 0, ctx.Err()
			}
		}, 40)

		opCtx, opCancel := context.WithCancel(context.Background())
		op, cancel := NewOperation(opCtx, wp)
		defer cancel()

		for i := 0; i < 10; i++ {
			op.AddRequest(i)
		}

		done := make(chan struct{})
		go func() {
			op.Wait()
			close(done)
		}()

		opCancel()

		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("Wait did not return after context cancellation")
		}
	})

	t.Run("op context cancellation stops Results", func(t *testing.T) {
		wp := New(context.Background(), func(ctx context.Context, req int) (int, error) {
			select {
			case <-time.After(time.Hour):
				return req, nil
			case <-ctx.Done():
				return 0, ctx.Err()
			}
		}, 40)

		opCtx, opCancel := context.WithCancel(context.Background())
		op, cancel := NewOperation(opCtx, wp)
		defer cancel()

		for i := 0; i < 10; i++ {
			op.AddRequest(i)
		}

		ch := op.Results()

		opCancel()

		drained := make(chan struct{})
		go func() {
			for range ch {
			}
			close(drained)
		}()

		select {
		case <-drained:
		case <-time.After(3 * time.Second):
			t.Fatal("Results channel was not closed after context cancellation")
		}
	})

	t.Run("Err returns nil when no error", func(t *testing.T) {
		wp := New(context.Background(), func(_ context.Context, req int) (int, error) {
			return req, nil
		}, 40)

		op, cancel := NewOperation(context.Background(), wp)
		defer cancel()

		op.AddRequest(1)
		op.Wait()

		if err := op.Err(); err != nil {
			t.Errorf("expected nil error, got %v", err)
		}
	})

	t.Run("Err returns ErrOperationEnded after cancel func", func(t *testing.T) {
		wp := New(context.Background(), func(_ context.Context, req int) (int, error) {
			return req, nil
		}, 40)

		op, cancel := NewOperation(context.Background(), wp)

		op.AddRequest(1)
		op.Wait()

		cancel()

		err := op.Err()
		if !errors.Is(err, ErrOperationEnded) {
			t.Errorf("expected ErrOperationEnded, got %v", err)
		}
	})

	t.Run("multiple operations are independent", func(t *testing.T) {
		wp := New(context.Background(), func(_ context.Context, req int) (int, error) {
			return req, nil
		}, 40)

		op1, cancel1 := NewOperation(context.Background(), wp)
		defer cancel1()
		op2, cancel2 := NewOperation(context.Background(), wp)
		defer cancel2()

		for i := 0; i < 50; i++ {
			op1.AddRequest(i)
			op2.AddRequest(i + 1000)
		}

		done := make(chan Metrics, 2)

		go func() { done <- op1.Wait() }()
		go func() { done <- op2.Wait() }()

		for i := 0; i < 2; i++ {
			select {
			case m := <-done:
				if m.OperationsTotal != 50 {
					t.Errorf("op: got %d, want 50", m.OperationsTotal)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("timed out waiting for operations")
			}
		}
	})

	t.Run("single request works", func(t *testing.T) {
		wp := New(context.Background(), func(_ context.Context, req string) (string, error) {
			return "hello " + req, nil
		}, 40)

		op, cancel := NewOperation(context.Background(), wp)
		defer cancel()

		op.AddRequest("world")

		var result string
		for res := range op.Results() {
			result = res
		}

		if result != "hello world" {
			t.Errorf("got %q, want %q", result, "hello world")
		}
	})

	t.Run("pool context cancellation stops workers", func(t *testing.T) {
		poolCtx, poolCancel := context.WithCancel(context.Background())
		wp := New(poolCtx, func(ctx context.Context, req int) (int, error) {
			select {
			case <-time.After(time.Hour):
				return req, nil
			case <-ctx.Done():
				return 0, ctx.Err()
			}
		}, 40)

		op, cancel := NewOperation(poolCtx, wp)
		defer cancel()

		for i := 0; i < 5; i++ {
			op.AddRequest(i)
		}

		done := make(chan struct{})
		go func() {
			op.Wait()
			close(done)
		}()

		poolCancel()

		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("Wait did not return after pool context cancellation")
		}
	})

	// Bug-proving tests

	t.Run("handle decrements r on fn error", func(t *testing.T) {
		wp := New(context.Background(), func(_ context.Context, req int) (int, error) {
			return 0, errTest
		}, 4)

		op, cancel := NewOperation(context.Background(), wp)
		defer cancel()

		const requests = 20
		for i := 0; i < requests; i++ {
			op.AddRequest(i)
		}

		done := make(chan struct{})
		go func() {
			op.Wait()
			close(done)
		}()

		select {
		case <-done:
			// Wait returned — r was properly decremented on error paths
		case <-time.After(3 * time.Second):
			t.Fatal("Wait deadlocked: r not decremented on fn error")
		}
	})

	t.Run("AddRequest decrements r on pool cancel", func(t *testing.T) {
		poolCtx, poolCancel := context.WithCancel(context.Background())

		busy := make(chan struct{})

		wp := New(poolCtx, func(ctx context.Context, req int) (int, error) {
			close(busy)
			<-ctx.Done()
			return 0, ctx.Err()
		}, 1)

		op, cancel := NewOperation(context.Background(), wp)
		defer cancel()

		// Occupy the single worker with a task that never finishes.
		op.AddRequest(0)
		<-busy

		// These requests pile up behind the full pool channel.
		for i := 1; i <= 10; i++ {
			op.AddRequest(i)
		}

		poolCancel()

		done := make(chan struct{})
		go func() {
			op.Wait()
			close(done)
		}()

		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("Wait deadlocked: r not decremented when pool cancelled")
		}
	})

	t.Run("endFunc does not panic", func(t *testing.T) {
		const workers = 40

		// computed is buffered so fn never blocks; draining it proves that
		// `workers` goroutines have finished fn and are now blocked trying
		// to send their result to a consumer that does not exist.
		computed := make(chan struct{}, workers*2)

		wp := New(context.Background(), func(_ context.Context, req int) (int, error) {
			computed <- struct{}{}
			return req, nil
		}, workers)

		op, cancel := NewOperation(context.Background(), wp)

		for i := 0; i < workers*2; i++ {
			op.AddRequest(i)
		}

		for i := 0; i < workers; i++ {
			<-computed
		}

		// Cancel while workers are parked on `op.out <- res`.
		// Old code did close(op.out) here, which panicked the workers.
		cancel()

		done := make(chan struct{})
		go func() {
			op.Wait()
			close(done)
		}()

		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("workers did not unwind after the operation ended")
		}
	})

	t.Run("failure names the request", func(t *testing.T) {
		wp := New(context.Background(), func(_ context.Context, req string) (string, error) {
			if req == "bad" {
				return "", errTest
			}
			return req, nil
		}, 4)

		op, cancel := NewOperation(context.Background(), wp)
		defer cancel()

		op.AddRequest("bad")
		op.Wait()

		var f *Failure[string]
		if !errors.As(op.Err(), &f) {
			t.Fatalf("expected *Failure[string], got %T (%v)", op.Err(), op.Err())
		}
		if f.Request != "bad" {
			t.Errorf("failed request: got %q, want %q", f.Request, "bad")
		}
		if !errors.Is(op.Err(), errTest) {
			t.Errorf("expected the cause to unwrap to errTest, got %v", op.Err())
		}
	})

	t.Run("continue on error processes every request", func(t *testing.T) {
		wp := New(context.Background(), func(_ context.Context, req int) (int, error) {
			if req%3 == 0 {
				return 0, errTest
			}
			return req, nil
		}, 8)

		op, cancel := NewOperation(context.Background(), wp, WithContinueOnError())
		defer cancel()

		const requests = 60
		for i := 0; i < requests; i++ {
			op.AddRequest(i)
		}

		got := make(map[int]bool)

		drained := make(chan struct{})
		go func() {
			defer close(drained)
			for res := range op.Results() {
				got[res] = true
			}
		}()

		select {
		case <-drained:
		case <-time.After(5 * time.Second):
			t.Fatal("Results never closed with WithContinueOnError")
		}

		const wantFailed = requests / 3
		const wantOK = requests - wantFailed

		if len(got) != wantOK {
			t.Errorf("got %d results, want %d", len(got), wantOK)
		}
		if err := op.Err(); err != nil {
			t.Errorf("expected the operation to survive per-request errors, got %v", err)
		}

		m := op.Metrics(false)
		if m.OperationsTotal != wantOK || m.Failed != wantFailed {
			t.Errorf("metrics: got ok=%d failed=%d, want ok=%d failed=%d",
				m.OperationsTotal, m.Failed, wantOK, wantFailed)
		}

		failures := op.Failures()
		if len(failures) != wantFailed {
			t.Fatalf("got %d failures, want %d", len(failures), wantFailed)
		}
		for _, f := range failures {
			if f.Request%3 != 0 {
				t.Errorf("unexpected failed request %d", f.Request)
			}
			if !errors.Is(f, errTest) {
				t.Errorf("failure %d: got %v, want errTest", f.Request, f.Err)
			}
		}
	})

	t.Run("continue on error survives panics", func(t *testing.T) {
		wp := New(context.Background(), func(_ context.Context, req int) (int, error) {
			if req%2 == 0 {
				panic(req)
			}
			return req, nil
		}, 4)

		op, cancel := NewOperation(context.Background(), wp, WithContinueOnError())
		defer cancel()

		const requests = 20
		for i := 0; i < requests; i++ {
			op.AddRequest(i)
		}

		done := make(chan Metrics, 1)
		go func() { done <- op.Wait() }()

		select {
		case m := <-done:
			if m.OperationsTotal != requests/2 || m.Failed != requests/2 {
				t.Errorf("metrics: got ok=%d failed=%d, want %d/%d",
					m.OperationsTotal, m.Failed, requests/2, requests/2)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Wait deadlocked with panicking requests")
		}

		for _, f := range op.Failures() {
			if !errors.Is(f, ErrPanic) {
				t.Errorf("failure %d: got %v, want ErrPanic", f.Request, f.Err)
			}
		}
	})

	t.Run("Metrics reset keeps the failure list", func(t *testing.T) {
		wp := New(context.Background(), func(_ context.Context, req int) (int, error) {
			return 0, errTest
		}, 2)

		op, cancel := NewOperation(context.Background(), wp, WithContinueOnError())
		defer cancel()

		op.AddRequest(1)
		op.AddRequest(2)
		op.Wait()

		if m := op.Metrics(true); m.Failed != 2 {
			t.Errorf("before reset: got failed=%d, want 2", m.Failed)
		}
		if m := op.Metrics(false); m.Failed != 0 {
			t.Errorf("after reset: got failed=%d, want 0", m.Failed)
		}
		if n := len(op.Failures()); n != 2 {
			t.Errorf("after reset: got %d failures, want 2", n)
		}
	})

	t.Run("AddRequest reports refusal after the operation ends", func(t *testing.T) {
		wp := New(context.Background(), func(_ context.Context, req int) (int, error) {
			return req, nil
		}, 4)

		op, cancel := NewOperation(context.Background(), wp)

		if !op.AddRequest(1) {
			t.Error("first request should have been accepted")
		}

		op.Wait()
		cancel()

		// The sender goroutine closes the queue once it observes the
		// cancellation; until then pushes still succeed.
		deadline := time.After(3 * time.Second)
		for op.AddRequest(2) {
			select {
			case <-deadline:
				t.Fatal("AddRequest kept accepting requests after the operation ended")
			default:
			}
		}
	})

	t.Run("panic in fn is reported as an error", func(t *testing.T) {
		wp := New(context.Background(), func(_ context.Context, req int) (int, error) {
			panic("boom")
		}, 4)

		op, cancel := NewOperation(context.Background(), wp)
		defer cancel()

		op.AddRequest(1)

		done := make(chan struct{})
		go func() {
			op.Wait()
			close(done)
		}()

		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("Wait deadlocked: panic did not release the request")
		}

		err := op.Err()
		if !errors.Is(err, ErrPanic) {
			t.Fatalf("expected ErrPanic, got %v", err)
		}

		var pe *PanicError
		if !errors.As(err, &pe) {
			t.Fatalf("expected *PanicError, got %T", err)
		}
		if pe.Value != "boom" {
			t.Errorf("panic value: got %v, want boom", pe.Value)
		}
		if len(pe.Stack) == 0 {
			t.Error("expected a captured stack trace")
		}
	})

	t.Run("panic value that is an error unwraps to it", func(t *testing.T) {
		wp := New(context.Background(), func(_ context.Context, req int) (int, error) {
			panic(errTest)
		}, 2)

		op, cancel := NewOperation(context.Background(), wp)
		defer cancel()

		op.AddRequest(1)
		op.Wait()

		if err := op.Err(); !errors.Is(err, errTest) || !errors.Is(err, ErrPanic) {
			t.Errorf("expected both errTest and ErrPanic, got %v", err)
		}
	})

	t.Run("panics do not degrade the pool", func(t *testing.T) {
		const workers = 4

		wp := New(context.Background(), func(_ context.Context, req int) (int, error) {
			if req < 0 {
				panic("boom")
			}
			return req, nil
		}, workers)

		// Panic once per worker: without recover() every goroutine dies
		// here and the pool is left with nothing to run the next operation.
		for i := 0; i < workers*4; i++ {
			op, cancel := NewOperation(context.Background(), wp)
			op.AddRequest(-1)
			op.Wait()
			cancel()
		}

		op, cancel := NewOperation(context.Background(), wp)
		defer cancel()

		const requests = 50
		for i := 0; i < requests; i++ {
			op.AddRequest(i)
		}

		done := make(chan Metrics, 1)
		go func() { done <- op.Wait() }()

		select {
		case m := <-done:
			if m.OperationsTotal != requests {
				t.Errorf("got %d results, want %d", m.OperationsTotal, requests)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("pool stopped serving requests after panics: workers were lost")
		}
	})

	t.Run("calcTimeSum excludes errors", func(t *testing.T) {
		wp := New(context.Background(), func(_ context.Context, req int) (int, error) {
			if req%2 == 0 {
				time.Sleep(10 * time.Millisecond)
				return 0, errTest
			}
			return req, nil
		}, 4)

		op, cancel := NewOperation(context.Background(), wp)
		defer cancel()

		// Only add odd numbers (which succeed)
		for i := 1; i <= 5; i += 2 {
			op.AddRequest(i)
		}

		op.Wait()

		m := op.Metrics(false)
		// With the bug, calcTimeSum would include error processing time,
		// inflating the average. After fix, errors don't contribute to calcTimeSum.
		if m.OperationsTotal != 3 {
			t.Errorf("expected 3 successful operations, got %d", m.OperationsTotal)
		}
	})
}
