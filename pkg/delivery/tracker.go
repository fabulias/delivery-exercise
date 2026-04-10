package delivery

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Tracker manages package state through a channel-based async state machine.
type Tracker struct {
	mu          sync.RWMutex
	packages    map[string]*Package
	timers      map[string]*time.Timer
	subscribers map[string][]chan Notification

	notifier        Notifier
	trackingUpdater TrackingUpdater
	logger          *slog.Logger

	eventCh         chan Event
	eventChSize     int
	timeoutDuration time.Duration

	ctx          context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup
	shutdownOnce sync.Once
}

// NewTracker creates and starts a Tracker with a notifier, a tracking system updater, and options
func NewTracker(ctx context.Context, notifier Notifier, updater TrackingUpdater, opts ...Option) *Tracker {
	t := &Tracker{
		packages:        make(map[string]*Package),
		timers:          make(map[string]*time.Timer),
		subscribers:     make(map[string][]chan Notification),
		notifier:        notifier,
		trackingUpdater: updater,
		logger:          slog.Default(),
		timeoutDuration: 7 * 24 * time.Hour,
		eventChSize:     100,
	}

	for _, opt := range opts {
		opt(t)
	}

	t.eventCh = make(chan Event, t.eventChSize)
	t.ctx, t.cancel = context.WithCancel(ctx)

	t.wg.Add(1)
	go t.processLoop()

	return t
}

// StartTracking registers a new package for lifecycle tracking.
func (t *Tracker) StartTracking(input PackageInput) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if _, exists := t.packages[input.PackageID]; exists {
		return ErrPackageExists
	}

	pkg := &Package{
		PackageID:       input.PackageID,
		CustomerID:      input.CustomerID,
		Destination:     input.Destination,
		CreatedAt:       input.CreatedAt,
		Status:          StatusCreated,
		UpdatedAt:       time.Now(),
		ProcessedEvents: make(map[EventType]bool),
	}
	t.packages[input.PackageID] = pkg

	timer := time.AfterFunc(t.timeoutDuration, func() {
		t.handleTimeout(input.PackageID)
	})
	t.timers[input.PackageID] = timer

	t.logger.Info("tracking started",
		slog.String("packageID", input.PackageID),
		slog.String("customerID", input.CustomerID),
		slog.String("destination", input.Destination),
	)
	return nil
}

// HandleEvent enqueues an event for async processing. It returns immediately
// after enqueueing; the actual state transition happens in processLoop.
func (t *Tracker) HandleEvent(event Event) error {
	select {
	case <-t.ctx.Done():
		return ErrTrackerShutdown
	case t.eventCh <- event:
		return nil
	}
}

// GetStatus returns a snapshot copy of the package state.
func (t *Tracker) GetStatus(packageID string) (Package, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	pkg, ok := t.packages[packageID]
	if !ok {
		return Package{}, ErrPackageNotFound
	}

	copied := *pkg
	return copied, nil
}

// Subscribe returns a channel that receives Notifications whenever the given
// customer's packages change status. The channel is buffered (size 10).
func (t *Tracker) Subscribe(customerID string) <-chan Notification {
	t.mu.Lock()
	defer t.mu.Unlock()

	ch := make(chan Notification, 10)
	t.subscribers[customerID] = append(t.subscribers[customerID], ch)
	return ch
}

// Shutdown signals the tracker to stop and waits for the processLoop to drain.
// It is safe to call Shutdown multiple times; only the first call takes effect.
func (t *Tracker) Shutdown() {
	t.shutdownOnce.Do(func() {
		t.cancel()
		t.wg.Wait()

		t.mu.Lock()
		for _, timer := range t.timers {
			timer.Stop()
		}
		for _, channels := range t.subscribers {
			for _, ch := range channels {
				close(ch)
			}
		}
		t.mu.Unlock()
	})
}

// processLoop is the single goroutine that consumes events from eventCh.
func (t *Tracker) processLoop() {
	defer t.wg.Done()
	for {
		select {
		case <-t.ctx.Done():
			return
		case event := <-t.eventCh:
			t.processEvent(event)
		}
	}
}

// processEvent applies a single event to the state machine.
// Errors are logged (not returned) because processing is async.
func (t *Tracker) processEvent(event Event) {
	targetStatus, ok := eventToStatus[event.Type]
	if !ok {
		t.logger.Error("invalid event type",
			slog.String("packageID", event.PackageID),
			slog.String("eventType", string(event.Type)),
		)
		return
	}

	t.mu.Lock()

	pkg, ok := t.packages[event.PackageID]
	if !ok {
		t.mu.Unlock()
		t.logger.Error("package not found during event processing",
			slog.String("packageID", event.PackageID),
		)
		return
	}

	if pkg.Status == StatusExpired {
		t.mu.Unlock()
		t.logger.Info("ignoring event for expired package",
			slog.String("eventType", string(event.Type)),
			slog.String("packageID", event.PackageID),
		)
		return
	}

	// Idempotency: skip already-processed events silently.
	if pkg.ProcessedEvents[event.Type] {
		t.mu.Unlock()
		t.logger.Info("duplicate event ignored",
			slog.String("packageID", event.PackageID),
			slog.String("eventType", string(event.Type)),
		)
		return
	}

	expectedNext, valid := validTransitions[pkg.Status]
	if !valid || expectedNext != targetStatus {
		t.mu.Unlock()
		t.logger.Error("invalid state transition",
			slog.String("packageID", event.PackageID),
			slog.String("currentStatus", string(pkg.Status)),
			slog.String("targetStatus", string(targetStatus)),
		)
		return
	}

	pkg.Status = targetStatus
	pkg.UpdatedAt = time.Now()
	pkg.ProcessedEvents[event.Type] = true

	if targetStatus == StatusDelivered {
		if timer, exists := t.timers[event.PackageID]; exists {
			timer.Stop()
			delete(t.timers, event.PackageID)
		}
	}

	// lock released here on purpose — had a deadlock when I held it across the notifier call
	packageID := pkg.PackageID
	customerID := pkg.CustomerID
	newStatus := pkg.Status

	t.mu.Unlock()

	if err := t.notifier.NotifyCustomer(t.ctx, packageID, newStatus); err != nil {
		t.logger.Error("notification failed",
			slog.String("packageID", packageID),
			slog.String("status", string(newStatus)),
			slog.Any("error", err),
		)
	}

	if newStatus == StatusInTransit || newStatus == StatusDelivered {
		if err := t.trackingUpdater.UpdateTrackingSystem(t.ctx, packageID, newStatus); err != nil {
			t.logger.Error("tracking update failed",
				slog.Any("error", err),
				slog.String("packageID", packageID),
				slog.String("status", string(newStatus)),
			)
		}
	}

	notification := Notification{
		PackageID:  packageID,
		CustomerID: customerID,
		Status:     newStatus,
		Timestamp:  time.Now(),
	}
	t.broadcastToSubscribers(customerID, notification)
}

// broadcastToSubscribers sends a notification to all registered channels for
// the given customer. Uses a non-blocking send to avoid stalling processLoop.
func (t *Tracker) broadcastToSubscribers(customerID string, notification Notification) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	for _, ch := range t.subscribers[customerID] {
		select {
		case ch <- notification:
		default:
			t.logger.Warn("subscriber channel full, dropping notification",
				slog.String("customerID", customerID),
				slog.String("packageID", notification.PackageID),
			)
		}
	}
}

// handleTimeout is called by time.AfterFunc when a package reaches its deadline.
func (t *Tracker) handleTimeout(packageID string) {
	t.mu.Lock()

	pkg, ok := t.packages[packageID]
	if !ok {
		t.mu.Unlock()
		return
	}

	if pkg.Status == StatusDelivered || pkg.Status == StatusExpired {
		t.mu.Unlock()
		return
	}

	pkg.Status = StatusExpired
	pkg.UpdatedAt = time.Now()

	customerID := pkg.CustomerID

	t.mu.Unlock()

	t.logger.Info("package expired",
		slog.String("packageID", packageID),
	)

	if err := t.notifier.NotifyCustomer(t.ctx, packageID, StatusExpired); err != nil {
		t.logger.Error("failed to notify customer of expiration",
			slog.String("packageID", packageID),
			slog.Any("error", fmt.Errorf("expiration notify: %v", err)),
		)
	}

	notification := Notification{
		PackageID:  packageID,
		CustomerID: customerID,
		Status:     StatusExpired,
		Timestamp:  time.Now(),
	}
	t.broadcastToSubscribers(customerID, notification)
}
