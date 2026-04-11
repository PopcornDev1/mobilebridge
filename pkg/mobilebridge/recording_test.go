package mobilebridge

import (
	"strings"
	"testing"
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
