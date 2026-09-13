package poly

// Option configures an operation created by [NewOperation].
// Options are applied in order; later options win.
type Option func(*config)

// config is the resolved set of options for a single operation.
type config struct {
	continueOnError bool
	maxQueue        int
	rejectOnFull    bool
}

func newConfig(opts []Option) config {
	var c config
	for _, opt := range opts {
		if opt != nil {
			opt(&c)
		}
	}
	return c
}

// WithContinueOnError switches the operation from fail-fast to
// collect-and-continue.
//
// By default the first failing request cancels the whole operation and
// every result computed so far is discarded — the right behaviour when
// the batch is all-or-nothing, the wrong one when you are grinding
// through a million records and a handful of them are simply bad.
//
// With this option a failing request is recorded in [Op.Failures] and
// [Metrics.Failed], and the remaining requests keep being processed.
// [Op.Err] stays nil: the operation did not fail, some of its requests
// did.
func WithContinueOnError() Option {
	return func(c *config) { c.continueOnError = true }
}

// WithMaxQueue bounds the operation's queue to n requests waiting to be
// handed to the pool.
//
// By default the queue is unbounded and [Op.AddRequest] never blocks: a
// producer that outruns the workers grows the queue until the process
// runs out of memory. Set a limit whenever the producer is not naturally
// paced — reading a file, scanning a table, consuming a stream.
//
// Once the queue is full, AddRequest blocks until the sender drains it,
// or until the operation or pool context is cancelled — see
// [WithRejectOnFull] for the non-blocking alternative. n <= 0 restores
// the unbounded default.
//
// The limit counts queued requests only. Requests already picked up by
// the operation's sender goroutine or sitting in the pool channel are
// not counted, so the real ceiling is a small constant factor above n.
func WithMaxQueue(n int) Option {
	return func(c *config) { c.maxQueue = n }
}

// WithRejectOnFull makes [Op.AddRequest] return false immediately
// instead of blocking when the queue set by [WithMaxQueue] is full.
// It has no effect on an unbounded queue.
//
// Use it when shedding load is better than slowing the producer down;
// the caller decides what to do with the refused request.
func WithRejectOnFull() Option {
	return func(c *config) { c.rejectOnFull = true }
}
