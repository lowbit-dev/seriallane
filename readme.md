# seriallane

Per-key job serialization. Jobs sharing a key run one at a time, jobs with different keys run in parallel. Lane lifecycle is managed automatically.

## Features

- **Per-key serialization**: Each key gets its own implicit mutex. Contention on one key does not block another.
- **Multi-key locking**: `DoMulti` acquires multiple lanes atomically in a consistent order, preventing deadlocks.
- **Automatic lifecycle**: Lanes are created on first use and removed after a configurable idle timeout.
- **Panic recovery**: Panics inside jobs are caught and returned as errors. The lane is left in a clean state.
- **Zero dependencies**: Only the Go standard library.

## Installation
```
go get lowbit.dev/seriallane
```

## Usage

```go
m := seriallane.New(10 * time.Minute)

// Single key — one goroutine at a time per orderID.
err := m.Do(ctx, seriallane.Namespace("order", orderID), func(ctx context.Context) error {
    return processOrder(ctx, orderID)
})

// Multiple keys — acquired atomically, no deadlock risk.
err = m.DoMulti(ctx, []seriallane.Key{keyA, keyB}, func(ctx context.Context) error {
    return swapStock(ctx, a, b)
})
```

## Keys

`Key` is a plain string type with two helpers for building structured keys:

```go
// Namespace builds "ns:id"
key := seriallane.Namespace("order", orderID)

// Sub appends a segment to an existing key: "order:123:line"
sub := key.Sub("line")
```

## Cleanup

Lanes accumulate as new keys are seen. Remove idle ones by calling `CleanupStaleLanes` directly, or run the built-in background service:

```go
svc := m.CleanupService(5 * time.Minute)
if err := svc.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
    log.Fatal(err)
}
```

`Run` ticks at the given interval and stops when `ctx` is cancelled. A lane is stale when it has not been used for longer than the `idleTimeout` passed to `New`.

## Errors

| Sentinel | When |
|---|---|
| `ErrPanicRecovered` | The job function panicked. The original value is in the error message. |
| `ErrCleanupFailed` | `CleanupStaleLanes` returned an error inside `CleanupService.Run`. |
