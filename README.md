# Package delivery tracking

It tracks packages through a simple lifecycle using a state machine.

---

## How to run

```bash
# Run all tests (with race detector)
go test -race -v ./...

# Run the demo
go run cmd/delivery/main.go

# Check coverage
go test -coverprofile=cover.out ./pkg/delivery/ && go tool cover -func=cover.out
```

---

## Approach

I went with a state machine (the exercise called it Option B). Each package moves through fixed states:

```
created -> pickup -> in_transit -> delivered
```

There is also an `expired` state, set by a timer if the package does not reach `delivered` in time (default 7 days). Out-of-order or repeated events are rejected or ignored, the status does not change.

I was not sure at first whether to use a simple struct with a mutex or channels. I went with channels because it makes the async nature explicit — `HandleEvent` enqueues and returns immediately, and one goroutine processes events in order. That avoids a whole class of bugs where two callers race to apply transitions.

---

## Main decisions

**Event channel.** `HandleEvent` drops the event into a buffered channel and returns. A single `processLoop` goroutine reads from it. This means events are always processed one at a time, in order.

**Lock released before side effects.** After updating the package status I release the mutex before calling the notifier or tracking system. I had a deadlock in an earlier version where I held the lock during the notifier call, and the notifier tried to read status. Now the lock only covers the map writes.

**Retry in the clients, not in Tracker.** The notifier and tracking system clients handle their own retries. The Tracker calls them once and logs the error if it fails. I think this is cleaner — the Tracker should not know about HTTP timeouts or backoff policies.

**One mutex.** I use a single `sync.RWMutex` for packages, timers, and subscribers. An earlier version had two separate mutexes (`mu` and `subMu`). Two mutexes for data that is touched in different goroutines sounds good in theory but in practice it made the shutdown code more complicated and the risk of accidentally holding both was not worth it for this size of project.

**Interfaces for dependencies.** `Notifier` and `TrackingUpdater` are injected interfaces. This is mostly for tests — it lets me swap in mocks without any framework.

---

## What is not perfect

The `ProcessedEvents` map is returned inside the `Package` struct by value but the map itself is a reference type. So a caller who gets a `Package` from `GetStatus` and mutates `ProcessedEvents` will corrupt internal state. I know this. I removed a defensive copy that was there before because it felt like over-engineering for this exercise, but in production I would either bring it back or make `ProcessedEvents` an unexported field.

---

## What I would add with more time

- Persistent storage so state survives restarts
- Metrics — at minimum a counter for events processed and errors
- A dead-letter queue for events that keep failing
- More edge cases in tests (e.g. Subscribe before StartTracking, very large concurrent load)

---

## Assumptions

- Transitions are strictly linear. `delivered` before `pickup` is rejected and logged.
- Side effects (notify, tracking update) are best-effort. A failure does not roll back the state change.
- Everything is in-memory. State is lost on restart.
- One package per ID. Registering an existing ID returns an error.
