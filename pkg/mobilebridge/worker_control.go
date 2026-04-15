package mobilebridge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type workerAttachedSession interface {
	BrowserURL() string
	Close() error
	Done() <-chan struct{}
}

type WorkerControlServer struct {
	addr       string
	listenAddr string

	mu            sync.Mutex
	httpSrv       *http.Server
	sessions      map[string]*workerControlSession
	startAttached func(context.Context, string, string) (workerAttachedSession, error)
	newSessionID  func() string
}

type workerControlSession struct {
	id       string
	deviceID string
	session  workerAttachedSession
}

type WorkerAttachRequest struct {
	DeviceID string `json:"device_id"`
}

type WorkerAttachResponse struct {
	SessionID string `json:"session_id"`
	DeviceID  string `json:"device_id"`
	Endpoint  string `json:"endpoint,omitempty"`
}

type WorkerCreateTargetRequest struct {
	URL string `json:"url"`
}

type WorkerCreateTargetResponse struct {
	ID                   string `json:"id"`
	Title                string `json:"title,omitempty"`
	URL                  string `json:"url,omitempty"`
	Type                 string `json:"type,omitempty"`
	DevtoolsFrontendURL  string `json:"devtoolsFrontendUrl,omitempty"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl,omitempty"`
}

func NewWorkerControlServer(addr string) *WorkerControlServer {
	return &WorkerControlServer{
		addr:     addr,
		sessions: make(map[string]*workerControlSession),
		startAttached: func(ctx context.Context, serial, addr string) (workerAttachedSession, error) {
			return StartAttachedServer(ctx, serial, addr)
		},
		newSessionID: func() string {
			return "mbw_" + randomSuffix(4)
		},
	}
}

func (s *WorkerControlServer) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/sessions", s.handleSessions)
	mux.HandleFunc("/sessions/", s.handleSession)

	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	s.mu.Lock()
	s.httpSrv = srv
	s.listenAddr = ln.Addr().String()
	s.mu.Unlock()
	go func() {
		_ = srv.Serve(ln)
	}()
	return nil
}

func (s *WorkerControlServer) Stop() error {
	s.mu.Lock()
	srv := s.httpSrv
	sessions := make([]*workerControlSession, 0, len(s.sessions))
	for _, session := range s.sessions {
		sessions = append(sessions, session)
	}
	s.mu.Unlock()

	var err error
	for _, session := range sessions {
		if closeErr := session.session.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}
	if srv == nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if shutdownErr := srv.Shutdown(ctx); shutdownErr != nil && err == nil {
		err = shutdownErr
	}
	return err
}

func (s *WorkerControlServer) handleSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req WorkerAttachRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if strings.TrimSpace(req.DeviceID) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "device_id is required"})
		return
	}
	port, err := freeTCPPort()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	session, err := s.startAttached(r.Context(), req.DeviceID, fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	entry := &workerControlSession{
		id:       s.newSessionID(),
		deviceID: req.DeviceID,
		session:  session,
	}
	s.mu.Lock()
	s.sessions[entry.id] = entry
	s.mu.Unlock()
	go s.watchSession(entry)

	writeJSON(w, http.StatusOK, WorkerAttachResponse{
		SessionID: entry.id,
		DeviceID:  entry.deviceID,
		Endpoint:  session.BrowserURL(),
	})
}

func (s *WorkerControlServer) handleSession(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/sessions/")
	if path == "" {
		http.NotFound(w, r)
		return
	}
	if strings.HasSuffix(path, "/targets") {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		sessionID := strings.TrimSuffix(path, "/targets")
		sessionID = strings.TrimSuffix(sessionID, "/")
		s.handleCreateTarget(w, r, sessionID)
		return
	}
	if strings.Contains(path, "/") {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodDelete {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	s.handleDeleteSession(w, r, path)
}

func (s *WorkerControlServer) handleDeleteSession(w http.ResponseWriter, r *http.Request, sessionID string) {
	entry := s.popSession(sessionID)
	if entry == nil {
		http.NotFound(w, r)
		return
	}
	if err := entry.session.Close(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "released"})
}

func (s *WorkerControlServer) handleCreateTarget(w http.ResponseWriter, r *http.Request, sessionID string) {
	entry := s.getSession(sessionID)
	if entry == nil {
		http.NotFound(w, r)
		return
	}
	var req WorkerCreateTargetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if strings.TrimSpace(req.URL) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "url is required"})
		return
	}

	target, err := createTargetViaBrowserURL(r.Context(), entry.session.BrowserURL(), req.URL)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, target)
}

func (s *WorkerControlServer) watchSession(entry *workerControlSession) {
	<-entry.session.Done()
	s.mu.Lock()
	current := s.sessions[entry.id]
	if current == entry {
		delete(s.sessions, entry.id)
	}
	s.mu.Unlock()
}

func (s *WorkerControlServer) getSession(id string) *workerControlSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[id]
}

func (s *WorkerControlServer) popSession(id string) *workerControlSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.sessions[id]
	delete(s.sessions, id)
	return entry
}

func createTargetViaBrowserURL(ctx context.Context, browserURL, targetURL string) (*WorkerCreateTargetResponse, error) {
	if strings.TrimSpace(browserURL) == "" {
		return nil, errors.New("mobilebridge: browser url is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	u := browserURL + "/json/new?url=" + url.QueryEscape(targetURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mobilebridge: target creation failed: status %d", resp.StatusCode)
	}
	var target WorkerCreateTargetResponse
	if err := json.NewDecoder(resp.Body).Decode(&target); err != nil {
		return nil, err
	}
	return &target, nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func randomSuffix(n int) string {
	if n <= 0 {
		n = 4
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "00000000"
	}
	return hex.EncodeToString(buf)
}
