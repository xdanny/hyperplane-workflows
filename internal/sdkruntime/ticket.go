package sdkruntime

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// RunTicket executes exactly one saved, ready Board task using the ordinary
// managed project-task runtime. Completion waits for human review; no retry or
// model reviewer can promote a ticket or start a dependent task automatically.
func RunTicket(ctx context.Context, st *store.Store, opts Options, source store.SDKTicketAdmission, platformRoot string) (result Result, runErr error) {
	admitted := false
	defer func() {
		if !admitted && opts.Started != nil {
			opts.Started <- runErr
		}
	}()
	target, err := filepath.Abs(opts.TargetRoot)
	if err != nil {
		return result, err
	}
	target, err = filepath.EvalSymlinks(target)
	if err != nil {
		return result, err
	}
	platform, err := filepath.Abs(platformRoot)
	if err != nil {
		return result, err
	}
	platform, err = filepath.EvalSymlinks(platform)
	if err != nil {
		return result, err
	}
	for _, pair := range [][2]string{{target, platform}, {platform, target}} {
		rel, err := filepath.Rel(pair[0], pair[1])
		if err != nil || filepath.IsLocal(rel) {
			return result, fmt.Errorf("task target must be disjoint from platform code")
		}
	}
	opts.TargetRoot = target
	opts.Project = source.Lane.Project
	opts.Goal = source.Ticket.Title
	opts.AutoRepair = false
	opts.ticket = &source
	admitted = true
	return RunCheckedTask(ctx, st, opts, source.Ticket.Title+"\n\n"+source.Ticket.Detail, nil)
}
