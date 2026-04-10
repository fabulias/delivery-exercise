package notifier

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"delivery-exercise/pkg/delivery"
)

type Notifier struct {
	logger         *slog.Logger
	maxRetries     int
	retryBaseDelay time.Duration
}

type Option func(*Notifier)

// WithMaxRetries sets the maximum number of notification retry attempts.
func WithMaxRetries(r int) Option {
	return func(n *Notifier) { n.maxRetries = r }
}

// WithRetryBaseDelay sets the base delay for exponential backoff retries.
func WithRetryBaseDelay(d time.Duration) Option {
	return func(n *Notifier) { n.retryBaseDelay = d }
}

// WithLogger sets the structured logger used by notifier.
func WithLogger(l *slog.Logger) Option {
	return func(n *Notifier) { n.logger = l }
}

// New creates a Notifier with the given options.
func New(opts ...Option) *Notifier {
	n := &Notifier{
		logger:         slog.Default(),
		maxRetries:     3,
		retryBaseDelay: 100 * time.Millisecond,
	}
	for _, opt := range opts {
		opt(n)
	}
	return n
}

// NotifyCustomer logs the status change and retries in case of errors.
func (n *Notifier) NotifyCustomer(ctx context.Context, packageID string, status delivery.Status) error {
	return n.withRetry(ctx, func() error {
		n.logger.Info("customer notified",
			slog.String("packageID", packageID),
			slog.String("status", string(status)),
		)
		return nil
	})
}

// withRetry executes fn with exponential backoff until maxRetries is reached.
func (n *Notifier) withRetry(ctx context.Context, fn func() error) error {
	var lastErr error
	delay := n.retryBaseDelay
	for i := 0; i < n.maxRetries; i++ {
		if err := fn(); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if i < n.maxRetries-1 {
			select {
			case <-ctx.Done():
				return fmt.Errorf("context cancelled during retry: %w", ctx.Err())
			case <-time.After(delay):
			}
			delay *= 2
		}
	}
	return fmt.Errorf("notification failed after %d attempts: %w", n.maxRetries, lastErr)
}
