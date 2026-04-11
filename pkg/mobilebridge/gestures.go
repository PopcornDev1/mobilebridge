package mobilebridge

import (
	"encoding/json"
	"fmt"
	"math"
	"sync/atomic"
	"time"
)

// TouchPoint mirrors the CDP Input.TouchPoint type.
type TouchPoint struct {
	X           float64 `json:"x"`
	Y           float64 `json:"y"`
	RadiusX     float64 `json:"radiusX,omitempty"`
	RadiusY     float64 `json:"radiusY,omitempty"`
	Force       float64 `json:"force,omitempty"`
	ID          float64 `json:"id,omitempty"`
	TangentialP float64 `json:"tangentialPressure,omitempty"`
}

// TouchEventParams is the payload for `Input.dispatchTouchEvent`.
type TouchEventParams struct {
	Type        string       `json:"type"` // touchStart | touchMove | touchEnd | touchCancel
	TouchPoints []TouchPoint `json:"touchPoints"`
}

// cdpMessage is the JSON wire format sent over the CDP websocket.
type cdpMessage struct {
	ID     int64           `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// messageSender is the subset of Proxy that gesture helpers need. Tests can
// substitute a fake implementation without needing a real websocket.
type messageSender interface {
	sendUpstream(method string, params any) error
}

// idSource provides CDP request ids. Shared across gestures on the same Proxy.
var globalID int64

func nextID() int64 { return atomic.AddInt64(&globalID, 1) }

// buildTouchEvent constructs a JSON-ready Input.dispatchTouchEvent payload.
func buildTouchEvent(kind string, points []TouchPoint) TouchEventParams {
	if points == nil {
		points = []TouchPoint{}
	}
	return TouchEventParams{Type: kind, TouchPoints: points}
}

// maxCoord is the upper bound we allow for touch coordinates. Real mobile
// viewports top out well below this; anything larger is almost certainly a
// caller bug (negative ints coerced to huge values, off-by-one, etc).
const maxCoord = 100000

func validCoord(x, y int) error {
	if x < 0 || y < 0 || x > maxCoord || y > maxCoord {
		return fmt.Errorf("mobilebridge: coord out of range: (%d, %d)", x, y)
	}
	return nil
}

// Tap sends a single-finger touchStart then touchEnd at (x, y).
func Tap(p messageSender, x, y int) error {
	if err := validCoord(x, y); err != nil {
		return err
	}
	points := []TouchPoint{{X: float64(x), Y: float64(y), ID: 0, Force: 1}}
	if err := p.sendUpstream("Input.dispatchTouchEvent", buildTouchEvent("touchStart", points)); err != nil {
		return err
	}
	return p.sendUpstream("Input.dispatchTouchEvent", buildTouchEvent("touchEnd", nil))
}

// LongPress sends a touchStart, waits durationMs, then sends a touchEnd.
func LongPress(p messageSender, x, y, durationMs int) error {
	if err := validCoord(x, y); err != nil {
		return err
	}
	if durationMs <= 0 {
		return fmt.Errorf("mobilebridge: long press duration must be > 0, got %d", durationMs)
	}
	points := []TouchPoint{{X: float64(x), Y: float64(y), ID: 0, Force: 1}}
	if err := p.sendUpstream("Input.dispatchTouchEvent", buildTouchEvent("touchStart", points)); err != nil {
		return err
	}
	time.Sleep(time.Duration(durationMs) * time.Millisecond)
	return p.sendUpstream("Input.dispatchTouchEvent", buildTouchEvent("touchEnd", nil))
}

// Swipe performs a single-finger drag from (fromX, fromY) to (toX, toY) over
// durationMs milliseconds, interpolating with a fixed number of move events.
func Swipe(p messageSender, fromX, fromY, toX, toY, durationMs int) error {
	const steps = 10
	if err := validCoord(fromX, fromY); err != nil {
		return err
	}
	if err := validCoord(toX, toY); err != nil {
		return err
	}
	if durationMs < 0 {
		durationMs = 0
	}
	start := []TouchPoint{{X: float64(fromX), Y: float64(fromY), ID: 0, Force: 1}}
	if err := p.sendUpstream("Input.dispatchTouchEvent", buildTouchEvent("touchStart", start)); err != nil {
		return err
	}
	stepSleep := time.Duration(durationMs) * time.Millisecond / time.Duration(steps+1)
	for i := 1; i <= steps; i++ {
		frac := float64(i) / float64(steps+1)
		x := float64(fromX) + (float64(toX-fromX) * frac)
		y := float64(fromY) + (float64(toY-fromY) * frac)
		move := []TouchPoint{{X: x, Y: y, ID: 0, Force: 1}}
		if err := p.sendUpstream("Input.dispatchTouchEvent", buildTouchEvent("touchMove", move)); err != nil {
			return err
		}
		if stepSleep > 0 {
			time.Sleep(stepSleep)
		}
	}
	end := []TouchPoint{{X: float64(toX), Y: float64(toY), ID: 0, Force: 1}}
	if err := p.sendUpstream("Input.dispatchTouchEvent", buildTouchEvent("touchMove", end)); err != nil {
		return err
	}
	return p.sendUpstream("Input.dispatchTouchEvent", buildTouchEvent("touchEnd", nil))
}

// Pinch performs a two-finger pinch centered at (centerX, centerY). A scale
// greater than 1 zooms in (fingers move apart); less than 1 zooms out.
func Pinch(p messageSender, centerX, centerY int, scale float64) error {
	if scale <= 0 {
		return fmt.Errorf("mobilebridge: pinch scale must be > 0, got %v", scale)
	}
	const steps = 10
	const baseOffset = 200.0 // starting half-distance between fingers in px

	// Starting finger positions (horizontal axis) and end positions.
	startOffset := baseOffset
	endOffset := baseOffset * scale

	startA := TouchPoint{X: float64(centerX) - startOffset, Y: float64(centerY), ID: 0, Force: 1}
	startB := TouchPoint{X: float64(centerX) + startOffset, Y: float64(centerY), ID: 1, Force: 1}

	if err := p.sendUpstream("Input.dispatchTouchEvent", buildTouchEvent("touchStart", []TouchPoint{startA, startB})); err != nil {
		return err
	}

	for i := 1; i <= steps; i++ {
		frac := float64(i) / float64(steps)
		off := startOffset + (endOffset-startOffset)*frac
		a := TouchPoint{X: float64(centerX) - off, Y: float64(centerY), ID: 0, Force: 1}
		b := TouchPoint{X: float64(centerX) + off, Y: float64(centerY), ID: 1, Force: 1}
		if err := p.sendUpstream("Input.dispatchTouchEvent", buildTouchEvent("touchMove", []TouchPoint{a, b})); err != nil {
			return err
		}
	}
	return p.sendUpstream("Input.dispatchTouchEvent", buildTouchEvent("touchEnd", nil))
}

// round is an internal helper for tests.
func round(f float64) float64 { return math.Round(f*1000) / 1000 }
