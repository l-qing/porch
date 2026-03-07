package main

import (
	"time"

	pipestatus "porch/pkg/pipeline"
)

func pipelineElapsedText(startedAt, completedAt, lastTransitionAt *time.Time, status pipestatus.Status, now time.Time) string {
	if startedAt == nil {
		return "-"
	}
	end := completedAt
	if end == nil && status != pipestatus.StatusRunning && status != pipestatus.StatusWatching && status != pipestatus.StatusPending {
		end = lastTransitionAt
	}
	if end == nil {
		end = &now
	}
	elapsed := end.Sub(*startedAt)
	if elapsed < 0 {
		elapsed = 0
	}
	return elapsed.Truncate(time.Second).String()
}

func cloneTimePtr(raw *time.Time) *time.Time {
	if raw == nil {
		return nil
	}
	cloned := *raw
	return &cloned
}
