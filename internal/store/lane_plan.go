package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// CreateLaneWithTickets publishes an entire planned lane atomically. Planning
// must finish before entering this short transaction; no model call holds it.
func (s *Store) CreateLaneWithTickets(ctx context.Context, lane *Lane, tickets []Ticket) error {
	if lane == nil || lane.ID == "" || len(tickets) == 0 || len(tickets) > 100 {
		return fmt.Errorf("a lane and 1–100 tickets are required")
	}
	seen := make(map[string]bool, len(tickets))
	for _, ticket := range tickets {
		if ticket.ID == "" || seen[ticket.ID] || ticket.LaneID != lane.ID {
			return fmt.Errorf("ticket identities must be unique and belong to the planned lane")
		}
		for _, dep := range ticket.Deps {
			if !seen[dep] {
				return fmt.Errorf("ticket dependencies must refer to earlier tickets in this plan")
			}
		}
		seen[ticket.ID] = true
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	project := strings.TrimSpace(lane.Project)
	if project != "" {
		if _, err := tx.Exec(ctx, `INSERT INTO projects (id, name) VALUES ($1,$2) ON CONFLICT (id) DO NOTHING`, project, defaultProjectName(project)); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO lanes (id,name,description,status,target_root,project,origin_run,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,NOW())`, lane.ID, lane.Name, lane.Description, lane.Status, lane.TargetRoot, project, lane.OriginRun); err != nil {
		return fmt.Errorf("create lane: %w", err)
	}
	for _, ticket := range tickets {
		deps := ticket.Deps
		if deps == nil {
			deps = []string{}
		}
		raw, err := json.Marshal(deps)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO tickets (id,lane_id,title,detail,status,priority,assignee,deps,output,error,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NOW(),NOW())`, ticket.ID, ticket.LaneID, ticket.Title, ticket.Detail, ticket.Status, ticket.Priority, ticket.Assignee, raw, ticket.Output, ticket.Error); err != nil {
			return fmt.Errorf("create ticket: %w", err)
		}
	}
	return tx.Commit(ctx)
}
