package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"delivery-exercise/pkg/delivery"
	"delivery-exercise/pkg/notifier"
	"delivery-exercise/pkg/trackingsystem"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	n := notifier.New(notifier.WithLogger(logger))
	u := trackingsystem.New(trackingsystem.WithLogger(logger))

	tracker := delivery.NewTracker(ctx, n, u,
		delivery.WithTimeoutDuration(7*24*time.Hour),
		delivery.WithLogger(logger),
	)
	defer tracker.Shutdown()

	notifications := tracker.Subscribe("CUST-001")

	// Start tracking a demo package.
	input := delivery.PackageInput{
		PackageID:   "PKG-001",
		CustomerID:  "CUST-001",
		Destination: "456 Oak Avenue, Springfield",
		CreatedAt:   time.Now(),
	}
	if err := tracker.StartTracking(input); err != nil {
		logger.Error("failed to start tracking", slog.Any("error", err))
		os.Exit(1)
	}
	logger.Info("package registered", slog.String("packageID", input.PackageID))

	go func() {
		for notif := range notifications {
			fmt.Printf("[SUBSCRIBER] Package %s -> %s at %s\n",
				notif.PackageID, notif.Status, notif.Timestamp.Format(time.RFC3339))
		}
	}()

	events := []delivery.Event{
		{PackageID: "PKG-001", Type: delivery.EventPickup, Timestamp: time.Now()},
		{PackageID: "PKG-001", Type: delivery.EventInTransit, Timestamp: time.Now()},
		{PackageID: "PKG-001", Type: delivery.EventDelivered, Timestamp: time.Now()},
	}

	for _, evt := range events {
		select {
		case <-ctx.Done():
			logger.Info("shutdown signal received, stopping demo")
			return
		default:
		}

		logger.Info("sending event", slog.String("type", string(evt.Type)))
		if err := tracker.HandleEvent(evt); err != nil {
			logger.Error("failed to handle event", slog.Any("error", err))
			os.Exit(1)
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Give the processLoop time to flush the last event.
	time.Sleep(200 * time.Millisecond)

	pkg, err := tracker.GetStatus("PKG-001")
	if err != nil {
		logger.Error("failed to get status", slog.Any("error", err))
		os.Exit(1)
	}
	logger.Info("final package status",
		slog.String("packageID", pkg.PackageID),
		slog.String("status", string(pkg.Status)),
		slog.String("updatedAt", pkg.UpdatedAt.Format(time.RFC3339)),
	)
}
