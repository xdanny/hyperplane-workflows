package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const consoleManagementBodyLimit = 1 << 20

// A console receives a revocable, session-local management capability. This mux
// deliberately never mounts the application's run, terminal or file handlers.
type consoleManagement struct {
	URL, Token string
	server     *http.Server
	cancel     context.CancelFunc
	done       chan struct{}
	closeOnce  sync.Once
}

func (s *Server) startConsoleManagement(parent context.Context) (*consoleManagement, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("create console capability: %w", err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("open console management listener: %w", err)
	}
	ctx, cancel := context.WithCancel(parent)
	bridge := &consoleManagement{URL: "http://" + listener.Addr().String(), Token: base64.RawURLEncoding.EncodeToString(secret), cancel: cancel, done: make(chan struct{})}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects", s.handleListProjects)
	mux.HandleFunc("POST /api/projects", s.handleCreateProject)
	mux.HandleFunc("GET /api/projects/{id}", s.handleGetProject)
	mux.HandleFunc("PATCH /api/projects/{id}", s.handlePatchProject)
	mux.HandleFunc("GET /api/workflows", s.handleListSDKWorkflows)
	mux.HandleFunc("POST /api/workflows", s.handleSaveSDKWorkflow)
	mux.HandleFunc("PUT /api/workflows/{id}", s.handleSaveSDKWorkflow)
	mux.HandleFunc("POST /api/workflows/validate", s.handleValidateSDKWorkflow)
	mux.HandleFunc("GET /api/workflow-checks", s.handleWorkflowChecks)
	bridge.server = &http.Server{
		ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 15 * time.Second,
		MaxHeaderBytes: 16 << 10,
		BaseContext:    func(net.Listener) context.Context { return ctx },
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			if len(r.Header.Values("Authorization")) != 1 || !strings.HasPrefix(header, "Bearer ") || subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(header, "Bearer ")), []byte(bridge.Token)) != 1 {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid console capability"})
				return
			}
			if r.Method != http.MethodGet && r.Method != http.MethodPost && r.Method != http.MethodPatch && r.Method != http.MethodPut {
				http.NotFound(w, r)
				return
			}
			if ctx.Err() != nil {
				writeJSON(w, http.StatusGone, map[string]string{"error": "console session ended"})
				return
			}
			// Consume the bounded body before calling existing handlers, including those
			// whose JSON decoder could otherwise stop before oversized trailing bytes.
			body, err := io.ReadAll(io.LimitReader(r.Body, consoleManagementBodyLimit+1))
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "could not read request"})
				return
			}
			if len(body) > consoleManagementBodyLimit {
				writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "console request exceeds 1 MiB"})
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			requestCtx, requestCancel := context.WithTimeout(r.Context(), 10*time.Second)
			defer requestCancel()
			mux.ServeHTTP(w, r.WithContext(requestCtx))
		}),
	}
	go func() { defer close(bridge.done); _ = bridge.server.Serve(listener) }()
	context.AfterFunc(ctx, bridge.Close)
	return bridge, nil
}

func (b *consoleManagement) Close() {
	b.closeOnce.Do(func() {
		b.cancel()
		// Cancellation reaches active DB/SDK-validation handlers before the listener
		// closes; shutdown waits for them before terminal ownership is released.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := b.server.Shutdown(ctx); err != nil {
			_ = b.server.Close()
		}
		<-b.done
	})
}
