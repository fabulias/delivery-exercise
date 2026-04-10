package delivery

import (
	"time"
)

// Status represents the lifecycle state of a package.
type Status string

const (
	StatusCreated   Status = "created"
	StatusPickup    Status = "pickup"
	StatusInTransit Status = "in_transit"
	StatusDelivered Status = "delivered"
	StatusExpired   Status = "expired"
)

// validTransitions defines allowed state machine edges.
var validTransitions = map[Status]Status{
	StatusCreated:   StatusPickup,
	StatusPickup:    StatusInTransit,
	StatusInTransit: StatusDelivered,
}

// EventType identifies the kind of event being processed.
type EventType string

const (
	EventPickup    EventType = "pickup"
	EventInTransit EventType = "in_transit"
	EventDelivered EventType = "delivered"
)

// eventToStatus maps an incoming event type to the target package status.
var eventToStatus = map[EventType]Status{
	EventPickup:    StatusPickup,
	EventInTransit: StatusInTransit,
	EventDelivered: StatusDelivered,
}

// PackageInput is the data required to begin tracking a new package.
type PackageInput struct {
	PackageID   string
	CustomerID  string
	Destination string
	CreatedAt   time.Time
}

// Package holds the full runtime state of a tracked package.
type Package struct {
	PackageID       string
	CustomerID      string
	Destination     string
	CreatedAt       time.Time
	Status          Status
	UpdatedAt       time.Time
	ProcessedEvents map[EventType]bool
}

// Event represents a status-change event submitted by a carrier or system.
type Event struct {
	PackageID string
	Type      EventType
	Timestamp time.Time
}

// Notification is pushed to subscriber channels when a package status changes.
type Notification struct {
	PackageID  string
	CustomerID string
	Status     Status
	Timestamp  time.Time
}
