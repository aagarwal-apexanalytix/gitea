// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package doctor

import (
	"context"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	"gitea.dev/modules/log"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/timeutil"
	actions_service "gitea.dev/services/actions"
)

// findStuckBlockedActionJobs returns Actions run jobs that have stayed in the Blocked state
// for longer than ABANDONED_JOB_TIMEOUT. A job whose `if:` expression failed to evaluate on
// the server can stay Blocked with nothing to retry it, which keeps the parent run Blocked and
// makes the repository Actions counter climb without bound. The scheduled cancel_abandoned_jobs
// task already reaps these, but only once ABANDONED_JOB_TIMEOUT (24h by default) has elapsed;
// this check lets an admin reap them on demand.
func findStuckBlockedActionJobs(ctx context.Context) ([]*actions_model.ActionRunJob, error) {
	return db.Find[actions_model.ActionRunJob](ctx, actions_model.FindRunJobOptions{
		Statuses:      []actions_model.Status{actions_model.StatusBlocked},
		UpdatedBefore: timeutil.TimeStampNow().AddDuration(-setting.Actions.AbandonedJobTimeout),
	})
}

func checkActionsStuckBlockedRuns(ctx context.Context, logger log.Logger, autofix bool) error {
	jobs, err := findStuckBlockedActionJobs(ctx)
	if err != nil {
		logger.Critical("Unable to find blocked Actions jobs: %v", err)
		return err
	}
	if len(jobs) == 0 {
		logger.Info("No Actions jobs are stuck in the blocked state")
		return nil
	}

	if !autofix {
		logger.Warn("%d Actions job(s) are stuck in the blocked state; run with --fix to cancel them", len(jobs))
		return nil
	}

	cancelled, err := actions_model.CancelJobs(ctx, jobs)
	if err != nil {
		logger.Critical("Unable to cancel blocked Actions jobs: %v", err)
		return err
	}
	// Cancelling the jobs rolls the parent attempt and run out of Blocked. Mirror the
	// cancel_abandoned_jobs task so watchers and any newly unblocked jobs are updated too.
	actions_service.NotifyWorkflowJobsAndRunsStatusUpdate(ctx, cancelled)
	actions_service.EmitJobsIfReadyByJobs(cancelled)
	logger.Info("Cancelled %d Actions job(s) stuck in the blocked state", len(cancelled))
	return nil
}

func init() {
	Register(&Check{
		Title:     "Cancel Actions runs stuck in the blocked state",
		Name:      "fix-actions-unfinished-run-status",
		IsDefault: false,
		Run:       checkActionsStuckBlockedRuns,
		Priority:  10,
	})
}
