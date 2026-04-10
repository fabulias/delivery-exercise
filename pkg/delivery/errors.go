package delivery

import "errors"

// Sentinel errors returned by Tracker methods.
var (
	ErrPackageNotFound   = errors.New("package not found")
	ErrPackageExists     = errors.New("package already exists")
	ErrInvalidTransition = errors.New("invalid state transition")
	ErrTrackerShutdown   = errors.New("tracker is shut down")
	ErrInvalidEvent      = errors.New("invalid event type")
)
