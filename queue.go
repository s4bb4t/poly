package poly

import "sync"

// sendQueue is a mutex-protected FIFO queue with a wake channel.
// A single sender goroutine waits on wake, then calls take to drain
// items in batches. Producers call push to enqueue an item and signal
// the sender.
//
// After close is called, push returns false and all remaining items
// are discarded.
type sendQueue[T any] struct {
	mu    sync.Mutex
	items []T
	done  bool
	wake  chan struct{} // capacity 1; coalesces multiple push signals
}

// newSendQueue returns a ready-to-use queue.
func newSendQueue[T any]() *sendQueue[T] {
	return &sendQueue[T]{wake: make(chan struct{}, 1)}
}

// push appends an item and wakes the sender goroutine.
// It returns false if the queue has been closed.
func (q *sendQueue[T]) push(item T) bool {
	q.mu.Lock()
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

// take atomically drains all queued items and returns them as a batch.
// The caller owns the returned slice; the internal slice is reset to nil.
func (q *sendQueue[T]) take() []T {
	q.mu.Lock()
	batch := q.items
	q.items = nil
	q.mu.Unlock()
	return batch
}

// close marks the queue as permanently closed and returns the number
// of items that were still queued (dropped). After close, push returns false.
func (q *sendQueue[T]) close() int {
	q.mu.Lock()
	q.done = true
	n := len(q.items)
	q.items = nil
	q.mu.Unlock()
	return n
}
