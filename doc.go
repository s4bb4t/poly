// Package poly provides a generic worker pool with independent operations.
//
// A [WorkerPool] executes a user-supplied function concurrently across a
// fixed number of goroutines. Callers create independent [Op] instances
// via [NewOperation]; each operation tracks its own pending requests,
// results, errors, and processing metrics.
//
// # Architecture
//
// The pool owns a channel of closures (in) and a set of worker goroutines
// that pull from it. Each [Op] has its own [sendQueue] — a single sender
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
// The pool lives until its context is cancelled. Operations are independent:
// each has its own context, and the end function returned by [NewOperation]
// cancels only that operation. Workers process requests from any active
// operation. When the pool context is cancelled, all workers exit, all
// sender goroutines drain their queues, and pending request counters are
// decremented so that [Op.Wait] and [Op.Results] unblock.
//
// # Error handling
//
// If the user function returns an error, the operation is cancelled with
// that error as cause. Subsequent requests for the same operation are
// dropped. The error is retrievable via [Op.Err].
package poly
