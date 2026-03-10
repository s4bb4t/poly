package poly_test

import (
	"context"
	"fmt"
	"sort"

	"github.com/s4bb4t/poly"
)

// Basic usage: create a pool, submit requests, collect results.
func Example() {
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

// Wait drains results internally and returns processing metrics.
func Example_wait() {
	wp := poly.New(context.Background(), func(_ context.Context, s string) (int, error) {
		return len(s), nil
	}, 4)

	op, end := poly.NewOperation(wp, context.Background())
	defer end()

	op.AddRequest("hello")
	op.AddRequest("world")

	m := op.Wait()
	fmt.Println("processed:", m.OperationsTotal)
	// Output: processed: 2
}

// Multiple independent operations can share a single pool.
func Example_multipleOperations() {
	wp := poly.New(context.Background(), func(_ context.Context, n int) (string, error) {
		if n%2 == 0 {
			return "even", nil
		}
		return "odd", nil
	}, 4)

	op1, end1 := poly.NewOperation(wp, context.Background())
	defer end1()

	op2, end2 := poly.NewOperation(wp, context.Background())
	defer end2()

	op1.AddRequest(2)
	op2.AddRequest(3)

	for v := range op1.Results() {
		fmt.Println("op1:", v)
	}
	for v := range op2.Results() {
		fmt.Println("op2:", v)
	}
	// Output:
	// op1: even
	// op2: odd
}
