package trackingsystem

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"delivery-exercise/pkg/delivery"
)

type Updater struct {
	logger         *slog.Logger
	maxRetries     int
	retryBaseDelay time.Duration
}

type Option func(*Updater)

// WithMaxRetries sets the maximum number of update retry attemps
func WithMaxRetries(n int) Option {
	return func(u *Updater) { u.maxRetries = n }
}

// WithRetryBaseDelay sets the base delay for exponential backoff retries
func WithRetryBaseDelay(d time.Duration) Option {
	return func(u *Updater) { u.retryBaseDelay = d }
}

// WithLogger sets the structured logger used by the updater
func WithLogger(l *slog.Logger) Option {
	return func(u *Updater) { u.logger = l }
}

func New(opts ...Option) *Updater {
	u := &Updater{
		logger:         slog.Default(),
		maxRetries:     3,
		retryBaseDelay: 100 * time.Millisecond,
	}
	for _, opt := range opts {
		opt(u)
	}
	return u
}

// UpdateTrackingSystem logs the status update and uses a retry logic internally.
func (u *Updater) UpdateTrackingSystem(ctx context.Context, packageID string, status delivery.Status) error {
	return u.withRetry(ctx, func() error {
		u.logger.Info("tracking system updated",
			slog.String("packageID", packageID),
			slog.String("status", string(status)),
		)
		return nil
	})
}

// withRetry executes fn with exponential backoff up to maxRetries attempts
func (u *Updater) withRetry(ctx context.Context, fn func() error) error {
	var lastErr error
	delay := u.retryBaseDelay
	for i := 0; i < u.maxRetries; i++ {
		if err := fn(); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if i < u.maxRetries-1 {
			select {
			case <-ctx.Done():
				return fmt.Errorf("context cancelled during retry: %w", ctx.Err())
			case <-time.After(delay):
			}
			delay *= 2
		}
	}
	return fmt.Errorf("tracking update failed after %d attempts: %w", u.maxRetries, lastErr)
}
