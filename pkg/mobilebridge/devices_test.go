package mobilebridge

import (
	"context"
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// scriptedRunner returns a commandRunner that yields consecutive stdout
// strings from a script on each invocation. After the script is exhausted
// the last entry is repeated.
func scriptedRunner(script []string) (func(ctx context.Context, name string, args ...string) ([]byte, error), *int32) {
	var idx int32
	var mu sync.Mutex
	return func(ctx context.Context, name string, args ...string) ([]byte, error) {
		mu.Lock()
		defer mu.Unlock()
		i := int(atomic.LoadInt32(&idx))
		if i >= len(script) {
			i = len(script) - 1
		}
		atomic.AddInt32(&idx, 1)
		return []byte(script[i]), nil
	}, &idx
}

func withStubbedADB(t *testing.T, runner func(ctx context.Context, name string, args ...string) ([]byte, error)) {
	t.Helper()
	origRunner := commandRunner
	origLookup := adbLookupFn
	origInterval := watchInterval
	commandRunner = runner
	adbLookupFn = func(string) (string, error) { return "/fake/adb", nil }
	watchInterval = 5 * time.Millisecond
	t.Cleanup(func() {
		commandRunner = origRunner
		adbLookupFn = origLookup
		watchInterval = origInterval
		_ = exec.ErrNotFound
	})
}

func collectEvents(t *testing.T, ch <-chan DeviceEvent, want int, timeout time.Duration) []DeviceEvent {
	t.Helper()
	var got []DeviceEvent
	deadline := time.After(timeout)
	for len(got) < want {
		select {
		case ev, ok := <-ch:
			if !ok {
				return got
			}
			got = append(got, ev)
		case <-deadline:
			return got
		}
	}
	return got
}

// TestWatchDevices_NoDuplicateAdds feeds the same device across multiple
// ticks and asserts we emit exactly one Added event, not one per tick.
func TestWatchDevices_NoDuplicateAdds(t *testing.T) {
	const line = "List of devices attached\nSERIAL_A    device usb:1 product:p model:M transport_id:1\n"
	runner, _ := scriptedRunner([]string{line, line, line, line, line})
	withStubbedADB(t, runner)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := WatchDevices(ctx)
	if err != nil {
		t.Fatalf("watch: %v", err)
	}

	// Collect for a few ticks then cancel.
	time.Sleep(40 * time.Millisecond)
	cancel()

	var addedCount int
	drainDeadline := time.After(200 * time.Millisecond)
drain:
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				break drain
			}
			if ev.Type == DeviceAdded && ev.Device.Serial == "SERIAL_A" {
				addedCount++
			}
		case <-drainDeadline:
			break drain
		}
	}
	if addedCount != 1 {
		t.Errorf("want 1 Added event for SERIAL_A, got %d", addedCount)
	}
}

// TestWatchDevices_ProperlyHandlesRemoves feeds [A], [], [A] and expects
// the full add/remove/add flicker to be reported in order.
func TestWatchDevices_ProperlyHandlesRemoves(t *testing.T) {
	const withA = "List of devices attached\nSERIAL_A    device usb:1 product:p model:M transport_id:1\n"
	const empty = "List of devices attached\n"
	runner, _ := scriptedRunner([]string{withA, empty, withA, withA, withA})
	withStubbedADB(t, runner)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := WatchDevices(ctx)
	if err != nil {
		t.Fatalf("watch: %v", err)
	}

	events := collectEvents(t, ch, 3, 500*time.Millisecond)
	if len(events) < 3 {
		t.Fatalf("want 3 events, got %d: %+v", len(events), events)
	}
	if events[0].Type != DeviceAdded || events[0].Device.Serial != "SERIAL_A" {
		t.Errorf("events[0] = %+v, want Added SERIAL_A", events[0])
	}
	if events[1].Type != DeviceRemoved || events[1].Device.Serial != "SERIAL_A" {
		t.Errorf("events[1] = %+v, want Removed SERIAL_A", events[1])
	}
	if events[2].Type != DeviceAdded || events[2].Device.Serial != "SERIAL_A" {
		t.Errorf("events[2] = %+v, want Added SERIAL_A", events[2])
	}
}

// TestWatchDevices_StateChange feeds a device that transitions from
// "unauthorized" to "device" and expects a remove+add pair.
func TestWatchDevices_StateChange(t *testing.T) {
	const unauth = "List of devices attached\nSERIAL_A    unauthorized usb:1 transport_id:1\n"
	const ready = "List of devices attached\nSERIAL_A    device usb:1 product:p model:M transport_id:1\n"
	runner, _ := scriptedRunner([]string{unauth, ready, ready, ready})
	withStubbedADB(t, runner)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := WatchDevices(ctx)
	if err != nil {
		t.Fatalf("watch: %v", err)
	}

	events := collectEvents(t, ch, 3, 500*time.Millisecond)
	if len(events) < 3 {
		t.Fatalf("want >=3 events, got %d: %+v", len(events), events)
	}
	// events[0]: Added (unauthorized)
	// events[1]: Removed (unauthorized, state changed)
	// events[2]: Added (device)
	if events[0].Type != DeviceAdded || events[0].Device.State != "unauthorized" {
		t.Errorf("events[0] = %+v", events[0])
	}
	if events[1].Type != DeviceRemoved {
		t.Errorf("events[1] = %+v want Removed", events[1])
	}
	if events[2].Type != DeviceAdded || events[2].Device.State != "device" {
		t.Errorf("events[2] = %+v want Added/device", events[2])
	}
}

// TestWatchDevices_CtxCancellation asserts the output channel is closed
// promptly after the context is canceled.
func TestWatchDevices_CtxCancellation(t *testing.T) {
	const line = "List of devices attached\nSERIAL_A    device usb:1 product:p model:M transport_id:1\n"
	runner, _ := scriptedRunner([]string{line})
	withStubbedADB(t, runner)

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := WatchDevices(ctx)
	if err != nil {
		t.Fatalf("watch: %v", err)
	}
	// Drain the initial Added so the sender isn't blocked.
	select {
	case <-ch:
	case <-time.After(200 * time.Millisecond):
	}
	cancel()
	select {
	case _, ok := <-ch:
		if ok {
			// A pending event is fine; check the next recv closes.
			select {
			case _, ok := <-ch:
				if ok {
					t.Errorf("expected channel closed, got another event")
				}
			case <-time.After(200 * time.Millisecond):
				t.Error("channel not closed within 200ms after cancel")
			}
		}
	case <-time.After(200 * time.Millisecond):
		t.Error("channel not closed within 200ms after cancel")
	}
}
