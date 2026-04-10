package delivery

import (
	"log/slog"
	"time"
)

// Option is a functional option for configuring a Tracker.
type Option func(*Tracker)

// WithTimeoutDuration sets how long before a package is marked expired.
func WithTimeoutDuration(d time.Duration) Option {
	return func(t *Tracker) { t.timeoutDuration = d }
}

// WithLogger sets the structured logger used by the tracker.
func WithLogger(l *slog.Logger) Option {
	return func(t *Tracker) { t.logger = l }
}

// WithEventBufferSize sets the capacity of the internal event channel.
func WithEventBufferSize(n int) Option {
	return func(t *Tracker) { t.eventChSize = n }
}
