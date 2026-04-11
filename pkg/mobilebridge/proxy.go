package mobilebridge

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Proxy owns one upstream CDP WebSocket connection to Chrome on the device
// plus, while Serve is running, exactly one downstream client WebSocket. It
// pumps frames bidirectionally and tears everything down on Close.
type Proxy struct {
	serial     string
	localPort  int
	remoteSock string

	upstream *websocket.Conn

	// writeMu serializes writes on the upstream connection. Gesture helpers
	// and the downstream->upstream pump both use it.
	writeMu sync.Mutex

	closeOnce sync.Once
	closed    chan struct{}
}

// NewProxy sets up adb forwarding to the given device's Chrome devtools
// socket, queries Chrome's /json/version endpoint to find the browser-level
// WebSocket URL, and dials it. It returns a ready-to-Serve Proxy.
func NewProxy(serial string, localPort int) (*Proxy, error) {
	sock, err := ChromeDevtoolsSocket(serial)
	if err != nil {
		return nil, fmt.Errorf("find devtools socket: %w", err)
	}
	if err := Forward(serial, localPort, sock); err != nil {
		return nil, fmt.Errorf("adb forward: %w", err)
	}

	wsURL, err := fetchBrowserWebSocketURL(fmt.Sprintf("http://127.0.0.1:%d", localPort))
	if err != nil {
		_ = Unforward(serial, localPort)
		return nil, fmt.Errorf("fetch browser ws url: %w", err)
	}

	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		_ = Unforward(serial, localPort)
		return nil, fmt.Errorf("dial upstream: %w", err)
	}

	return &Proxy{
		serial:     serial,
		localPort:  localPort,
		remoteSock: sock,
		upstream:   conn,
		closed:     make(chan struct{}),
	}, nil
}

// browserVersionInfo is the subset of /json/version we care about.
type browserVersionInfo struct {
	Browser              string `json:"Browser"`
	ProtocolVersion      string `json:"Protocol-Version"`
	UserAgent            string `json:"User-Agent"`
	V8Version            string `json:"V8-Version"`
	WebKitVersion        string `json:"WebKit-Version"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

func fetchBrowserWebSocketURL(base string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	u.Path = "/json/version"
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(u.String())
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("json/version status %d: %s", resp.StatusCode, string(body))
	}
	var v browserVersionInfo
	if err := json.Unmarshal(body, &v); err != nil {
		return "", fmt.Errorf("decode json/version: %w", err)
	}
	if v.WebSocketDebuggerURL == "" {
		return "", errors.New("json/version: no webSocketDebuggerUrl")
	}
	return v.WebSocketDebuggerURL, nil
}

// sendUpstream serializes params to JSON and writes one CDP message. This
// satisfies the messageSender interface so gesture helpers can drive it.
func (p *Proxy) sendUpstream(method string, params any) error {
	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return err
		}
		raw = b
	}
	msg := cdpMessage{ID: nextID(), Method: method, Params: raw}
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	if p.upstream == nil {
		return errors.New("mobilebridge: proxy not connected")
	}
	return p.upstream.WriteMessage(websocket.TextMessage, b)
}

// Serve pumps frames in both directions until either side hangs up. It must
// be called at most once per Proxy.
func (p *Proxy) Serve(downstream *websocket.Conn) error {
	if downstream == nil {
		return errors.New("mobilebridge: nil downstream")
	}
	errCh := make(chan error, 2)

	// Downstream -> upstream. Intercept synthetic MobileBridge.* methods
	// and translate them into real CDP calls; forward everything else.
	go func() {
		for {
			mt, data, err := downstream.ReadMessage()
			if err != nil {
				errCh <- err
				return
			}
			if mt != websocket.TextMessage {
				continue
			}
			if handled, herr := p.maybeHandleSynthetic(data); handled {
				if herr != nil {
					errCh <- herr
					return
				}
				continue
			}
			p.writeMu.Lock()
			werr := p.upstream.WriteMessage(websocket.TextMessage, data)
			p.writeMu.Unlock()
			if werr != nil {
				errCh <- werr
				return
			}
		}
	}()

	// Upstream -> downstream.
	go func() {
		for {
			mt, data, err := p.upstream.ReadMessage()
			if err != nil {
				errCh <- err
				return
			}
			if err := downstream.WriteMessage(mt, data); err != nil {
				errCh <- err
				return
			}
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-p.closed:
		return nil
	}
}

// maybeHandleSynthetic peeks at an incoming CDP message and, if it names a
// MobileBridge.* gesture method, dispatches it via the gesture helpers.
// Returns handled=true if the message was consumed (regardless of error).
func (p *Proxy) maybeHandleSynthetic(raw []byte) (bool, error) {
	var probe struct {
		ID     int64           `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false, nil
	}
	if !strings.HasPrefix(probe.Method, "MobileBridge.") {
		return false, nil
	}
	switch probe.Method {
	case "MobileBridge.tap":
		var args struct{ X, Y int }
		if err := json.Unmarshal(probe.Params, &args); err != nil {
			return true, err
		}
		return true, Tap(p, args.X, args.Y)
	case "MobileBridge.longPress":
		var args struct {
			X, Y, DurationMs int
		}
		if err := json.Unmarshal(probe.Params, &args); err != nil {
			return true, err
		}
		return true, LongPress(p, args.X, args.Y, args.DurationMs)
	case "MobileBridge.swipe":
		var args struct {
			FromX, FromY, ToX, ToY, DurationMs int
		}
		if err := json.Unmarshal(probe.Params, &args); err != nil {
			return true, err
		}
		return true, Swipe(p, args.FromX, args.FromY, args.ToX, args.ToY, args.DurationMs)
	case "MobileBridge.pinch":
		var args struct {
			CenterX, CenterY int
			Scale            float64
		}
		if err := json.Unmarshal(probe.Params, &args); err != nil {
			return true, err
		}
		return true, Pinch(p, args.CenterX, args.CenterY, args.Scale)
	}
	return true, fmt.Errorf("mobilebridge: unknown synthetic method %q", probe.Method)
}

// Close tears down the upstream WebSocket and removes the adb forward.
func (p *Proxy) Close() error {
	var err error
	p.closeOnce.Do(func() {
		close(p.closed)
		if p.upstream != nil {
			err = p.upstream.Close()
		}
		if p.serial != "" && p.localPort != 0 {
			if uerr := Unforward(p.serial, p.localPort); uerr != nil && err == nil {
				err = uerr
			}
		}
	})
	return err
}

// Upstream exposes the underlying connection for advanced callers. It is not
// safe for concurrent writes; use sendUpstream instead.
func (p *Proxy) Upstream() *websocket.Conn { return p.upstream }
