package retrier

import (
	"context"
	"fmt"
	"math"
	"time"

	"porch/pkg/component"
	"porch/pkg/gh"
)

func BackoffDuration(initial time.Duration, multiplier float64, max time.Duration, attempt int) time.Duration {
	// Attempt starts from 1, so the first retry uses `initial` directly.
	if attempt <= 1 {
		if initial > max {
			return max
		}
		return initial
	}
	if multiplier < 1 {
		multiplier = 1
	}
	// Clamp the exponential result to max to prevent unbounded wait durations.
	pow := math.Pow(multiplier, float64(attempt-1))
	d := time.Duration(float64(initial) * pow)
	if d > max {
		return max
	}
	return d
}

func RediscoverPipelineRun(ctx context.Context, ghc *gh.Client, repo, sha, pipeline string) (namespace, pipelinerun string, err error) {
	runs, err := ghc.CheckRuns(ctx, repo, sha)
	if err != nil {
		return "", "", err
	}
	// Rediscovery intentionally ignores the previous run and picks the latest
	// logical check-run for the pipeline on the current commit.
	r, ok := component.FindPipelineCheckRun(runs, pipeline)
	if !ok {
		return "", "", fmt.Errorf("pipeline %q not found in check-runs", pipeline)
	}
	ns, run, _ := component.ParseDetailsURLForPipeline(r.DetailsURL, pipeline)
	if ns == "" || run == "" {
		return "", "", fmt.Errorf("failed to parse details_url for %s", pipeline)
	}
	return ns, run, nil
}
