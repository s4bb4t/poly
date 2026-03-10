package poly_test

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/s4bb4t/poly"
)

// Create a pool of 4 workers that squares integers, then collect all results.
func ExampleNew() {
	wp := poly.New(context.Background(), func(_ context.Context, n int) (int, error) {
		return n * n, nil
	}, 4)

	op, end := poly.NewOperation(wp, context.Background())
	defer end()

	for i := 1; i <= 5; i++ {
		op.AddRequest(i)
	}

	var results []int
	for v := range op.Results() {
		results = append(results, v)
	}

	sort.Ints(results)
	fmt.Println(results)
	// Output: [1 4 9 16 25]
}

// Two independent operations share a single pool. Each operation
// tracks its own requests and results separately.
func ExampleNewOperation() {
	wp := poly.New(context.Background(), func(_ context.Context, n int) (int, error) {
		return n * 10, nil
	}, 4)

	op1, end1 := poly.NewOperation(wp, context.Background())
	defer end1()
	op2, end2 := poly.NewOperation(wp, context.Background())
	defer end2()

	op1.AddRequest(1)
	op2.AddRequest(2)

	m1 := op1.Wait()
	m2 := op2.Wait()

	fmt.Println("op1 processed:", m1.OperationsTotal)
	fmt.Println("op2 processed:", m2.OperationsTotal)
	// Output:
	// op1 processed: 1
	// op2 processed: 1
}

func ExampleOp_AddRequest() {
	wp := poly.New(context.Background(), func(_ context.Context, s string) (string, error) {
		return "hello " + s, nil
	}, 4)

	op, end := poly.NewOperation(wp, context.Background())
	defer end()

	op.AddRequest("world")

	for v := range op.Results() {
		fmt.Println(v)
	}
	// Output: hello world
}

// Wait blocks until every submitted request has been processed,
// then returns the accumulated metrics.
func ExampleOp_Wait() {
	wp := poly.New(context.Background(), func(_ context.Context, n int) (int, error) {
		return n, nil
	}, 4)

	op, end := poly.NewOperation(wp, context.Background())
	defer end()

	for i := 0; i < 10; i++ {
		op.AddRequest(i)
	}

	m := op.Wait()
	fmt.Println("total:", m.OperationsTotal)
	// Output: total: 10
}

// Results returns a channel that yields each successful result.
// The channel closes when all requests are consumed.
func ExampleOp_Results() {
	wp := poly.New(context.Background(), func(_ context.Context, n int) (int, error) {
		return n * 2, nil
	}, 4)

	op, end := poly.NewOperation(wp, context.Background())
	defer end()

	op.AddRequest(3)
	op.AddRequest(7)

	var results []int
	for v := range op.Results() {
		results = append(results, v)
	}

	sort.Ints(results)
	fmt.Println(results)
	// Output: [6 14]
}

// Err returns the error that cancelled the operation, or nil.
func ExampleOp_Err() {
	myErr := errors.New("something went wrong")

	wp := poly.New(context.Background(), func(_ context.Context, n int) (int, error) {
		if n == 1 {
			return 0, myErr
		}
		return n, nil
	}, 1)

	op, end := poly.NewOperation(wp, context.Background())
	defer end()

	op.AddRequest(1)
	op.Wait()

	fmt.Println(op.Err())
	// Output: something went wrong
}

// Err returns ErrOperationEnded after the end function is called.
func ExampleOp_Err_ended() {
	wp := poly.New(context.Background(), func(_ context.Context, n int) (int, error) {
		return n, nil
	}, 4)

	op, end := poly.NewOperation(wp, context.Background())

	op.AddRequest(1)
	op.Wait()
	end()

	fmt.Println(errors.Is(op.Err(), poly.ErrOperationEnded))
	// Output: true
}

// Metrics returns processing statistics. Pass true to reset counters
// atomically after reading.
func ExampleOp_Metrics() {
	wp := poly.New(context.Background(), func(_ context.Context, n int) (int, error) {
		return n, nil
	}, 4)

	op, end := poly.NewOperation(wp, context.Background())
	defer end()

	for i := 0; i < 5; i++ {
		op.AddRequest(i)
	}
	op.Wait()

	m := op.Metrics(true) // read and reset
	fmt.Println("total:", m.OperationsTotal)
	fmt.Println("avg > 0:", m.AverageProcessingDuration > 0)

	after := op.Metrics(false) // counters were reset
	fmt.Println("after reset:", after.OperationsTotal)
	// Output:
	// total: 5
	// avg > 0: true
	// after reset: 0
}
