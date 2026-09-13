# poly

[![Go Reference](https://pkg.go.dev/badge/github.com/s4bb4t/poly.svg)](https://pkg.go.dev/github.com/s4bb4t/poly)
[![Go Report Card](https://goreportcard.com/badge/github.com/s4bb4t/poly)](https://goreportcard.com/report/github.com/s4bb4t/poly)
![Go 1.23+](https://img.shields.io/badge/go-1.23%2B-00ADD8)
![Zero dependencies](https://img.shields.io/badge/dependencies-0-brightgreen)

A generic worker pool for Go where one fixed set of goroutines serves many
**independent operations**. Each operation has its own requests, results,
errors and metrics; the workers are shared.

Reach for it when you have a large batch of homogeneous work — resolving
addresses, hitting an API, parsing records — that you want processed with a
bounded amount of concurrency, and you need per-batch results and error
reporting rather than one global pipeline.

```
go get github.com/s4bb4t/poly
```

Zero dependencies, Go 1.23+.

## Quick start

```go
wp := poly.New(context.Background(), func(_ context.Context, n int) (int, error) {
	return n * n, nil
}, 4)

op, end := poly.NewOperation(context.Background(), wp)
defer end()

for i := 1; i <= 5; i++ {
	op.AddRequest(i)
}
op.Done() // no more requests

var results []int
for v := range op.Results() {
	results = append(results, v)
}

sort.Ints(results)
fmt.Println(results) // [1 4 9 16 25]
```

`Wait()` is the same thing without the results: it drains them internally and
returns the accumulated `Metrics`.

## Model

```
        AddRequest ──► per-Op queue ──► Op sender goroutine ──┐
                                                              ▼
   Op A ─┐                                            wp.in (chan func())
   Op B ─┼── share ──►  WorkerPool: N goroutines  ◄────────────┘
   Op C ─┘                       │
                                 └──► results back to the originating Op
```

One pool, N workers, any number of concurrent operations. Requests are
batched into the pool channel by a single sender goroutine per operation —
there is no goroutine per request, and no map lookup on the hot path.

## Contract

| Call | Meaning |
|------|---------|
| `New(ctx, fn, n)` | start `n` workers; they live until `ctx` is cancelled |
| `NewOperation(ctx, wp, opts...)` | independent batch; `end()` cancels **this** operation |
| `op.AddRequest(req)` | submit; returns `false` if refused |
| `op.Done()` | **no more requests will be submitted** |
| `op.Results()` / `op.Wait()` | consume; finish after `Done()` + all requests accounted for |
| `op.Err()` / `op.Failures()` | what went wrong, and on which request |

`Done()` is not optional. Without it, "all submitted requests are consumed"
is indistinguishable from "nothing has been submitted yet", and `Results()`
would close in the gap between two batches. Call `AddRequest` from wherever
you like, then `Done()` exactly once, after the last one.

## Errors

Fail-fast by default: the first failing request cancels the operation and
`Err()` reports which one it was.

```go
if err := op.Err(); err != nil {
	var f *poly.Failure[Address]
	if errors.As(err, &f) {
		log.Printf("address %q: %v", f.Request, f.Err)
	}
}
```

`Failure` unwraps to the original error, so `errors.Is(op.Err(), io.EOF)`
still works. A panic inside your function is recovered, wrapped in
`*poly.PanicError` (panic value + stack) and reported the same way — it
never escapes into a worker goroutine.

For batch jobs where a few bad records should not sink the run:

```go
op, end := poly.NewOperation(ctx, wp, poly.WithContinueOnError())
defer end()

// ... AddRequest / Done ...

m := op.Wait() // m.OperationsTotal, m.Failed — Err() stays nil
for _, f := range op.Failures() {
	log.Printf("%v: %v", f.Request, f.Err)
}
```

## Backpressure

The per-operation queue is **unbounded by default** and `AddRequest` never
blocks — a producer faster than the workers will grow it until the process
runs out of memory. Bound it explicitly:

```go
poly.NewOperation(ctx, wp, poly.WithMaxQueue(1024))                        // blocks when full
poly.NewOperation(ctx, wp, poly.WithMaxQueue(1024), poly.WithRejectOnFull()) // refuses when full
```

The limit counts queued requests, not requests already handed to the pool,
so the real ceiling is a small constant above it.

## Limitations

- **`Done()` is required.** An operation that is never `Done` unblocks only
  on cancellation.
- **Unbounded queue by default.** Opt into `WithMaxQueue` for streaming
  producers.
- **`Wait()` and `Results()` are mutually exclusive** on the same operation,
  and there must be exactly one consumer.
- **Results are unordered.** They arrive as workers finish; correlate via the
  response type if you need to match them to inputs.
- **`end()` must be called** (usually `defer`) — it stops the operation's
  sender goroutine. It cancels the operation; it is not a completion signal.
- **`fn` runs concurrently** on `n` goroutines and must be safe for that.
- **Cancellation discards in-flight results.** Fail-fast throws away what has
  already been computed; use `WithContinueOnError` if you need to keep it.

## Development

```
make test    # -race + coverage
make race    # -count=20 across GOMAXPROCS 1/2/8
make check   # fmt + vet + lint + test
```

## License

MIT — see [LICENSE](LICENSE).
