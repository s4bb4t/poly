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
