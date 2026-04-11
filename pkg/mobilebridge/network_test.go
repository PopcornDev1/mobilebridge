package mobilebridge

import (
	"encoding/json"
	"testing"
)

// captureSender records every (method, params) pair sent upstream so tests
// can inspect the payload shape without a real WebSocket.
type captureSender struct {
	calls []capturedCall
	err   error
}

type capturedCall struct {
	Method string
	Params json.RawMessage
}

func (c *captureSender) sendUpstream(method string, params interface{}) error {
	if c.err != nil {
		return c.err
	}
	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return err
		}
		raw = b
	}
	c.calls = append(c.calls, capturedCall{Method: method, Params: raw})
	return nil
}

// TestBuildNetworkConditions_PayloadShape exercises the unit conversion
// without touching a Proxy at all.
func TestBuildNetworkConditions_PayloadShape(t *testing.T) {
	nc := buildNetworkConditions(false, 150, 1600, 750)
	// 1600 kbps -> 1600 * 1000 / 8 = 200000 B/s
	if nc.DownloadThroughput != 200000 {
		t.Errorf("download throughput = %v, want 200000", nc.DownloadThroughput)
	}
	// 750 kbps -> 93750 B/s
	if nc.UploadThroughput != 93750 {
		t.Errorf("upload throughput = %v, want 93750", nc.UploadThroughput)
	}
	if nc.Latency != 150 {
		t.Errorf("latency = %v, want 150", nc.Latency)
	}
	if nc.Offline {
		t.Errorf("offline should be false")
	}
}

func TestBuildNetworkConditions_OfflineAndZeros(t *testing.T) {
	nc := buildNetworkConditions(true, -1, 0, 0)
	if !nc.Offline {
		t.Error("offline not set")
	}
	if nc.Latency != 0 || nc.DownloadThroughput != 0 || nc.UploadThroughput != 0 {
		t.Errorf("want zeros, got %+v", nc)
	}
}

// TestEmulateNetworkConditions_PayloadShape routes EmulateNetworkConditions
// through a Proxy whose sendUpstream is replaced via method dispatch into a
// capture sender. Because EmulateNetworkConditions expects *Proxy, we build
// a real Proxy but never connect it — we stub out the upstream by
// intercepting via the method registry pattern used in iteration 1.
//
// This test specifically verifies:
//   - Network.enable is sent first
//   - Network.emulateNetworkConditions is sent second with the converted
//     byte-per-second throughput values.
func TestEmulateNetworkConditions_PayloadShape(t *testing.T) {
	cap := &captureSender{}
	// Call the internal helper that mirrors EmulateNetworkConditions but
	// takes a messageSender so we can avoid the real Proxy wiring.
	if err := emulateNetworkConditionsOn(cap, false, 200, 1000, 500); err != nil {
		t.Fatalf("emulate: %v", err)
	}
	if len(cap.calls) != 2 {
		t.Fatalf("want 2 calls, got %d", len(cap.calls))
	}
	if cap.calls[0].Method != "Network.enable" {
		t.Errorf("first call = %q, want Network.enable", cap.calls[0].Method)
	}
	if cap.calls[1].Method != "Network.emulateNetworkConditions" {
		t.Errorf("second call = %q", cap.calls[1].Method)
	}
	var nc NetworkConditions
	if err := json.Unmarshal(cap.calls[1].Params, &nc); err != nil {
		t.Fatalf("unmarshal params: %v", err)
	}
	if nc.DownloadThroughput != 125000 {
		t.Errorf("download = %v, want 125000", nc.DownloadThroughput)
	}
	if nc.UploadThroughput != 62500 {
		t.Errorf("upload = %v, want 62500", nc.UploadThroughput)
	}
	if nc.Latency != 200 {
		t.Errorf("latency = %v", nc.Latency)
	}
}
