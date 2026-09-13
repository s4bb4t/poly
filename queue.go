package poly

import "sync"

// sendQueue is a mutex-protected FIFO with a wake channel. A single
// sender goroutine waits on wake and then calls take to drain items in
// batches; producers call push to enqueue an item and signal the sender.
//
// When limit is positive the queue is bounded: a producer that finds it
// full either blocks until the sender drains it (block == true) or is
// refused immediately. limit <= 0 means unbounded.
//
// After close is called, push returns false, blocked producers are
// released, and all remaining items are discarded.
type sendQueue[T any] struct {
	mu    sync.Mutex
	full  *sync.Cond
	items []T
	limit int
	block bool
	done  bool
	wake  chan struct{} // capacity 1; coalesces multiple push signals
}

// newSendQueue returns a ready-to-use queue.
func newSendQueue[T any](limit int, block bool) *sendQueue[T] {
	q := &sendQueue[T]{
		limit: limit,
		block: block,
		wake:  make(chan struct{}, 1),
	}
	q.full = sync.NewCond(&q.mu)

	return q
}

// push appends an item and wakes the sender goroutine. It returns false
// if the queue has been closed, or if it is full and the queue was
// configured to refuse rather than block.
func (q *sendQueue[T]) push(item T) bool {
	q.mu.Lock()

	for q.limit > 0 && len(q.items) >= q.limit && !q.done {
		if !q.block {
			q.mu.Unlock()
			return false
		}

		q.full.Wait()
	}

	if q.done {
		q.mu.Unlock()
		return false
	}

	q.items = append(q.items, item)
	q.mu.Unlock()

	select {
	case q.wake <- struct{}{}:
	default:
	}

	return true
}

// take atomically drains all queued items and returns them as a batch,
// releasing any producers waiting for room. The caller owns the returned
// slice; the internal slice is reset to nil.
func (q *sendQueue[T]) take() []T {
	q.mu.Lock()
	batch := q.items
	q.items = nil
	q.full.Broadcast()
	q.mu.Unlock()

	return batch
}

// close marks the queue as permanently closed, releases every waiting
// producer and returns the number of items that were still queued
// (and are now dropped). After close, push returns false.
func (q *sendQueue[T]) close() int {
	q.mu.Lock()
	q.done = true
	n := len(q.items)
	q.items = nil
	q.full.Broadcast()
	q.mu.Unlock()

	return n
}
