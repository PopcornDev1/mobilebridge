package mobilebridge

import (
	"context"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestStartScreenRecording_CommandShape(t *testing.T) {
	args := buildScreenRecordArgs("R58N12ABCDE", "/sdcard/test.mp4")
	got := strings.Join(args, " ")
	want := "-s R58N12ABCDE shell screenrecord --time-limit 180 --bit-rate 4000000 /sdcard/test.mp4"
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestStartScreenRecording_EmptySerial(t *testing.T) {
	p := &Proxy{serial: ""}
	if err := p.StartScreenRecording(nil, "/tmp/out.mp4"); err == nil {
		t.Error("expected error for empty serial")
	}
}

func TestStartScreenRecording_EmptyOutputPath(t *testing.T) {
	p := &Proxy{serial: "R58N12ABCDE"}
	if err := p.StartScreenRecording(nil, ""); err == nil {
		t.Error("expected error for empty output path")
	}
}

func TestStopScreenRecording_NoActive(t *testing.T) {
	// Stopping a recording that was never started is a no-op.
	p := &Proxy{serial: "R58N12ABCDE"}
	if err := p.StopScreenRecording(nil); err != nil {
		t.Errorf("stop on idle proxy: %v", err)
	}
}

// TestRecording_SurvivesCallerContextCancel verifies that cancelling the
// caller's context used in StartScreenRecording does NOT kill the underlying
// subprocess. The recording should only stop when StopScreenRecording is
// invoked (which calls the stored cancel func). This is what makes tool-call
// / HTTP-handler ctx cancellation safe for long-lived recordings.
func TestRecording_SurvivesCallerContextCancel(t *testing.T) {
	origBuilder := screenRecordCmdBuilder
	t.Cleanup(func() { screenRecordCmdBuilder = origBuilder })

	// Substitute a fake builder that spawns `sleep 30` instead of adb. The
	// child honours the ctx passed here, so we can assert end-to-end that
	// the recording's ctx is detached from the caller's ctx.
	screenRecordCmdBuilder = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sleep", "30")
	}

	p := &Proxy{serial: "R58N12ABCDE"}

	callerCtx, cancelCaller := context.WithCancel(context.Background())
	if err := p.StartScreenRecording(callerCtx, "/tmp/out.mp4"); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		// Best-effort cleanup: clear package-level state so other tests
		// don't see our fake recording hanging around.
		screenRecordMu.Lock()
		if rec, ok := screenRecords[p]; ok {
			rec.cancel()
			_ = rec.cmd.Wait()
			delete(screenRecords, p)
		}
		screenRecordMu.Unlock()
	})

	// Sanity: recording is registered.
	screenRecordMu.Lock()
	rec, ok := screenRecords[p]
	screenRecordMu.Unlock()
	if !ok {
		t.Fatal("recording was not registered in screenRecords map")
	}

	// Kill the caller ctx. A buggy implementation that reused ctx as parent
	// would propagate this to the subprocess and kill it.
	cancelCaller()

	// Give the cancellation a chance to propagate if it were going to.
	time.Sleep(100 * time.Millisecond)

	// The sleep process must still be alive. ProcessState is nil until Wait
	// returns; Process.Signal(0) on a live process returns nil.
	if rec.cmd.ProcessState != nil {
		t.Fatal("subprocess exited after caller ctx cancel; recording did not detach from caller ctx")
	}
	if rec.cmd.Process == nil {
		t.Fatal("rec.cmd.Process is nil")
	}
	// syscall.Signal(0) is the standard "is it alive" probe on Unix.
	if err := rec.cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("process no longer alive: %v — caller ctx cancel propagated", err)
	}
}
