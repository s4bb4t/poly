# poly

Generic worker pool with independent operations.

## Build & test

```bash
make test          # -race + coverage
make race          # -count=20 across GOMAXPROCS 1/2/8
go vet ./...
```

## Package layout

| File | Purpose |
|------|---------|
| `doc.go` | Package-level documentation |
| `poly.go` | `WorkerPool`, `New`, `NewOperation`, `handle`, `call` |
| `op.go` | `Op` (operation handle), `AddRequest`, `Done`, `Wait`, `Results`, `Metrics`, `Err`, `Failures` |
| `options.go` | `Option`, `WithContinueOnError`, `WithMaxQueue`, `WithRejectOnFull` |
| `errors.go` | `ErrOperationEnded`, `ErrPanic`, `PanicError`, `Failure` |
| `queue.go` | `sendQueue` — internal FIFO used by each Op's sender goroutine |
| `example_test.go` | Testable examples (runnable via `go test`) |
| `poly_test.go` | Unit tests |

## Key design decisions

- **`in chan func()`** — workers execute closures, no routing/map lookup needed.
- **One sender goroutine per Op** — `AddRequest` pushes to a `sendQueue`; the sender batches items into `wp.in`. No goroutine-per-request.
- **No `close(op.out)`** — end function only cancels the context; channel is GC'd. Avoids send-on-closed-channel panics.
- **Merged context in `handle`** — `context.AfterFunc` propagates pool cancellation into op-scoped context so `fn` sees both signals.
- **`recover` in `wp.call`** — a panic in the user function becomes a `*PanicError` on the operation; it must never unwind a worker goroutine.
- **`r` counter** — tracks in-flight requests. Decremented by the consumer on success, by `Op.release` on every path that produces no result. `release` also pokes the coalescing `progress` channel so a parked consumer re-checks.
- **Explicit completion** — `Op.Done` sets `closed` and closes `noMore`; `finished()` loads `closed` *before* `r` (that ordering is what makes the check sound).
- **Failure records** — `*Failure[ReqType]` pairs the request with its error; it is the context cause in fail-fast mode and the element type of `Op.Failures()`.

## Conventions

- Go doc comments on all exported symbols; follow stdlib style.
- No embedded mutexes — use named `mu` field.
- Zero dependencies: standard library only.
- Tests must pass under `-race`; synchronise with channels, never `time.Sleep`.
- `errTest` is the shared sentinel in tests.
