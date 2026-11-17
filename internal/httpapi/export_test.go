package httpapi

import (
	"context"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/brief"
	"github.com/xdanny/hyperplane-workflows/internal/persona"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// SetPersonaCodex replaces the engine persona reviews run on — the same
// test-only seam SetBriefEngine is, for the same reasons.
func (s *Server) SetPersonaCodex(fn persona.CodexFunc) { s.personaCodex = fn }

// SetBriefEngine replaces the engine outcome briefs are written by.
//
// Test-only: a brief write is a codex call, and a test that spawns codex bills
// money, needs the binary on PATH, and takes minutes. Injecting the answer is
// the same seam internal/brief offers directly: the whole path runs, only the
// model response is replaced.
func (s *Server) SetBriefEngine(fn brief.CodexFunc) { s.briefCodex = fn }

// DrainRuns exercises the same shutdown lifecycle as the API executable.
func (s *Server) DrainRuns(timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return s.Shutdown(ctx) == nil
}

// Store exposes the server's store so a test can seed the records a feature
// reads. Test-only: production code reaches the store through handlers.
func (s *Server) Store() *store.Store { return s.store }
