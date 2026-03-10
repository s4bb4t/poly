# poly

Generic worker pool with independent operations.

## Build & test

```bash
make test          # tests + coverage
go test ./...      # quick run (uses build tag !race)
go vet ./...
```

## Package layout

| File | Purpose |
|------|---------|
| `doc.go` | Package-level documentation |
| `poly.go` | `WorkerPool`, `New`, `NewOperation`, `handle` |
| `op.go` | `Op` (operation handle), `Wait`, `Results`, `Metrics`, `Err` |
| `queue.go` | `sendQueue` — internal FIFO used by each Op's sender goroutine |
| `example_test.go` | Testable examples (runnable via `go test`) |
| `poly_test.go` | Unit tests (build tag `!race`) |

## Key design decisions

- **`in chan func()`** — workers execute closures, no routing/map lookup needed.
- **One sender goroutine per Op** — `AddRequest` pushes to a `sendQueue`; the sender batches items into `wp.in`. No goroutine-per-request.
- **No `close(op.out)`** — end function only cancels the context; channel is GC'd. Avoids send-on-closed-channel panics.
- **Merged context in `handle`** — `context.AfterFunc` propagates pool cancellation into op-scoped context so `fn` sees both signals.
- **`r` counter** — tracks in-flight requests. Decremented by consumer on success, by `handle` on error, by sender on cancellation. `Wait`/`Results` spin until `r == 0`.

## Conventions

- Go doc comments on all exported symbols; follow stdlib style.
- No embedded mutexes — use named `mu` field.
- Tests use `!race` build tag.
- `errTest` is the shared sentinel in tests.
