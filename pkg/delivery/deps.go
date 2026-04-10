package delivery

import "context"

// Notifier sends status change notifications to customers.
type Notifier interface {
	NotifyCustomer(ctx context.Context, packageID string, status Status) error
}

// TrackingUpdater syncs package status with the external tracking system.
type TrackingUpdater interface {
	UpdateTrackingSystem(ctx context.Context, packageID string, status Status) error
}
