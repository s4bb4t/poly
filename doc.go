// Package poly provides a generic worker pool with independent operations.
//
// A [WorkerPool] executes one user-supplied function concurrently across a
// fixed number of goroutines. Callers group work into independent [Op]
// instances created by [NewOperation]; each operation carries its own
// requests, results, failures and metrics, while sharing the pool's
// workers with every other operation.
//
// # Usage
//
//	wp := poly.New(ctx, resolve, 20)
//
//	op, end := poly.NewOperation(ctx, wp)
//	defer end()
//
//	go func() {
//		for _, addr := range addresses {
//			op.AddRequest(addr)
//		}
//		op.Done() // no more requests
//	}()
//
//	for res := range op.Results() {
//		// ...
//	}
//	if err := op.Err(); err != nil {
//		// ...
//	}
//
// # Architecture
//
// The pool owns a channel of closures (in) and a set of worker goroutines
// that pull from it. Each [Op] has its own sendQueue — a single sender
// goroutine batches requests and feeds them into the pool channel.
// This avoids spawning a goroutine per request.
//
//	Pool (N workers)
//	  ^
//	  | wp.in <- func()
//	  |
//	Op sender goroutine
//	  ^
//	  | q.push(req)
//	  |
//	AddRequest
//
// # Lifecycle
//
// The pool lives until its context is cancelled. Operations are
// independent: each has its own context, and the end function returned by
// [NewOperation] cancels only that operation. When the pool context is
// cancelled, all workers exit, all sender goroutines drain their queues,
// and pending request counters are released so that [Op.Wait] and
// [Op.Results] unblock.
//
// Completion is explicit. [Op.Done] states that no further requests will
// be submitted; only then can [Op.Wait] and [Op.Results] tell "everything
// is processed" apart from "nothing has been submitted yet". An operation
// that is never Done is only ever unblocked by cancellation.
//
// # Error handling
//
// By default an operation is fail-fast: the first request that returns an
// error cancels the operation, later requests are dropped, and [Op.Err]
// reports a [*Failure] carrying both the error and the request that
// produced it. [WithContinueOnError] switches to collecting failures and
// carrying on; they are then read back with [Op.Failures].
//
// A panic in the user-supplied function is recovered and reported as a
// [*PanicError] through the same path — it never escapes into the worker
// goroutine.
//
// # Backpressure
//
// An operation's queue is unbounded by default and [Op.AddRequest] never
// blocks, so a producer faster than the workers will grow the queue until
// memory runs out. [WithMaxQueue] bounds it and makes AddRequest block;
// [WithRejectOnFull] makes it refuse instead.
package poly
