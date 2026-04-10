package delivery

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type NotifyCall struct {
	PackageID string
	Status    Status
}

// MockNotifier records calls to NotifyCustomer. FailCount controls how many
// leading calls return an error (for retry testing).
type MockNotifier struct {
	mu        sync.Mutex
	Calls     []NotifyCall
	FailCount int
}

func (m *MockNotifier) NotifyCustomer(_ context.Context, packageID string, status Status) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = append(m.Calls, NotifyCall{PackageID: packageID, Status: status})
	if m.FailCount > 0 {
		m.FailCount--
		return fmt.Errorf("transient notify error")
	}
	return nil
}

func (m *MockNotifier) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.Calls)
}

type UpdateCall struct {
	PackageID string
	Status    Status
}

// MockTrackingUpdater records calls to UpdateTrackingSystem.
type MockTrackingUpdater struct {
	mu    sync.Mutex
	Calls []UpdateCall
}

func (m *MockTrackingUpdater) UpdateTrackingSystem(_ context.Context, packageID string, status Status) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = append(m.Calls, UpdateCall{PackageID: packageID, Status: status})
	return nil
}

func (m *MockTrackingUpdater) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.Calls)
}

// newTestTracker creates a Tracker with fast defaults suited for unit tests.
func newTestTracker(t *testing.T, opts ...Option) (*Tracker, *MockNotifier, *MockTrackingUpdater) {
	t.Helper()
	notifier := &MockNotifier{}
	updater := &MockTrackingUpdater{}

	defaults := []Option{
		WithTimeoutDuration(1 * time.Second),
	}
	allOpts := append(defaults, opts...)

	tracker := NewTracker(context.Background(), notifier, updater, allOpts...)
	t.Cleanup(tracker.Shutdown)
	return tracker, notifier, updater
}

func sampleInput(packageID string) PackageInput {
	return PackageInput{
		PackageID:   packageID,
		CustomerID:  "CUST-1",
		Destination: "123 Main St",
		CreatedAt:   time.Now(),
	}
}

func TestHappyPath(t *testing.T) {
	tracker, notifier, updater := newTestTracker(t)

	require.NoError(t, tracker.StartTracking(sampleInput("PKG-001")))

	require.NoError(t, tracker.HandleEvent(Event{PackageID: "PKG-001", Type: EventPickup, Timestamp: time.Now()}))
	require.NoError(t, tracker.HandleEvent(Event{PackageID: "PKG-001", Type: EventInTransit, Timestamp: time.Now()}))
	require.NoError(t, tracker.HandleEvent(Event{PackageID: "PKG-001", Type: EventDelivered, Timestamp: time.Now()}))

	require.Eventually(t, func() bool {
		pkg, err := tracker.GetStatus("PKG-001")
		return err == nil && pkg.Status == StatusDelivered
	}, 2*time.Second, 10*time.Millisecond)

	require.Eventually(t, func() bool {
		return notifier.callCount() == 3
	}, 2*time.Second, 10*time.Millisecond)

	assert.Equal(t, 3, notifier.callCount(), "NotifyCustomer should be called 3 times")
	assert.Equal(t, 2, updater.callCount(), "UpdateTrackingSystem called for in_transit + delivered")

	notifier.mu.Lock()
	defer notifier.mu.Unlock()
	assert.Equal(t, StatusPickup, notifier.Calls[0].Status)
	assert.Equal(t, StatusInTransit, notifier.Calls[1].Status)
	assert.Equal(t, StatusDelivered, notifier.Calls[2].Status)
}

func TestTimeoutExpires(t *testing.T) {
	tracker, _, _ := newTestTracker(t, WithTimeoutDuration(100*time.Millisecond))

	require.NoError(t, tracker.StartTracking(sampleInput("PKG-002")))

	require.Eventually(t, func() bool {
		pkg, err := tracker.GetStatus("PKG-002")
		return err == nil && pkg.Status == StatusExpired
	}, 2*time.Second, 10*time.Millisecond)
}

func TestInvalidTransitionRejected(t *testing.T) {
	tracker, notifier, updater := newTestTracker(t)

	require.NoError(t, tracker.StartTracking(sampleInput("PKG-003")))

	// Send a "delivered" event on a package that is still in "created" state.
	require.NoError(t, tracker.HandleEvent(Event{PackageID: "PKG-003", Type: EventDelivered, Timestamp: time.Now()}))

	require.Eventually(t, func() bool {
		// the event was processed if the channel drained; we check state didn't change
		pkg, err := tracker.GetStatus("PKG-003")
		// we know it processed when status is still created and notifier hasn't been called
		return err == nil && pkg.Status == StatusCreated && notifier.callCount() == 0
	}, 2*time.Second, 10*time.Millisecond)

	pkg, err := tracker.GetStatus("PKG-003")
	require.NoError(t, err)
	assert.Equal(t, StatusCreated, pkg.Status, "invalid transition should be rejected; status unchanged")
	assert.Equal(t, 0, notifier.callCount(), "no notification on invalid transition")
	assert.Equal(t, 0, updater.callCount(), "no tracking update on invalid transition")
}

func TestDuplicateEventsIgnored(t *testing.T) {
	tracker, notifier, _ := newTestTracker(t)

	require.NoError(t, tracker.StartTracking(sampleInput("PKG-004")))

	require.NoError(t, tracker.HandleEvent(Event{PackageID: "PKG-004", Type: EventPickup, Timestamp: time.Now()}))
	require.NoError(t, tracker.HandleEvent(Event{PackageID: "PKG-004", Type: EventPickup, Timestamp: time.Now()}))

	require.Eventually(t, func() bool {
		pkg, err := tracker.GetStatus("PKG-004")
		return err == nil && pkg.Status == StatusPickup
	}, 2*time.Second, 10*time.Millisecond)

	// give time for both events to be processed
	require.Eventually(t, func() bool {
		return notifier.callCount() >= 1
	}, 2*time.Second, 10*time.Millisecond)

	assert.Equal(t, 1, notifier.callCount(), "duplicate event should not trigger a second notification")

	pkg, err := tracker.GetStatus("PKG-004")
	require.NoError(t, err)
	assert.Equal(t, StatusPickup, pkg.Status)
}

func TestSubscriberReceives(t *testing.T) {
	tracker, _, _ := newTestTracker(t)

	require.NoError(t, tracker.StartTracking(sampleInput("PKG-005")))
	ch := tracker.Subscribe("CUST-1")

	require.NoError(t, tracker.HandleEvent(Event{PackageID: "PKG-005", Type: EventPickup, Timestamp: time.Now()}))

	select {
	case notif := <-ch:
		assert.Equal(t, "PKG-005", notif.PackageID)
		assert.Equal(t, StatusPickup, notif.Status)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for subscriber notification")
	}
}

func TestNotifyFailureDoesNotBlockTransition(t *testing.T) {
	tracker, notifier, _ := newTestTracker(t)

	// Notifier always fails; retry is the client's responsibility, not the Tracker's.
	notifier.FailCount = 10

	require.NoError(t, tracker.StartTracking(sampleInput("PKG-007")))
	require.NoError(t, tracker.HandleEvent(Event{PackageID: "PKG-007", Type: EventPickup, Timestamp: time.Now()}))

	// State transition must succeed regardless of notification failure.
	require.Eventually(t, func() bool {
		pkg, err := tracker.GetStatus("PKG-007")
		return err == nil && pkg.Status == StatusPickup
	}, 2*time.Second, 10*time.Millisecond)

	// Tracker calls NotifyCustomer exactly once; retry is delegated to the client.
	assert.Equal(t, 1, notifier.callCount())
}

func TestConcurrentPackages(t *testing.T) {
	const n = 50

	tracker, notifier, updater := newTestTracker(t)

	for i := range n {
		pkgID := fmt.Sprintf("PKG-CONC-%d", i)
		require.NoError(t, tracker.StartTracking(PackageInput{
			PackageID:   pkgID,
			CustomerID:  fmt.Sprintf("CUST-%d", i),
			Destination: "Dest",
			CreatedAt:   time.Now(),
		}))
	}

	var sendWg sync.WaitGroup
	for i := range n {
		sendWg.Add(1)
		go func(i int) {
			defer sendWg.Done()
			pkgID := fmt.Sprintf("PKG-CONC-%d", i)
			_ = tracker.HandleEvent(Event{PackageID: pkgID, Type: EventPickup, Timestamp: time.Now()})
			_ = tracker.HandleEvent(Event{PackageID: pkgID, Type: EventInTransit, Timestamp: time.Now()})
			_ = tracker.HandleEvent(Event{PackageID: pkgID, Type: EventDelivered, Timestamp: time.Now()})
		}(i)
	}
	sendWg.Wait()

	require.Eventually(t, func() bool {
		return notifier.callCount() == n*3
	}, 5*time.Second, 10*time.Millisecond)

	assert.Equal(t, n*3, notifier.callCount())
	assert.Equal(t, n*2, updater.callCount())

	for i := range n {
		pkg, err := tracker.GetStatus(fmt.Sprintf("PKG-CONC-%d", i))
		require.NoError(t, err)
		assert.Equal(t, StatusDelivered, pkg.Status)
	}
}
