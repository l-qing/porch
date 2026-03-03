package retrier

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"porch/pkg/gh"
)

func TestBackoffDuration(t *testing.T) {
	initial := time.Minute
	max := 5 * time.Minute

	if got := BackoffDuration(initial, 1.5, max, 1); got != time.Minute {
		t.Fatalf("attempt1 = %s", got)
	}
	if got := BackoffDuration(initial, 1.5, max, 2); got != 90*time.Second {
		t.Fatalf("attempt2 = %s", got)
	}
	if got := BackoffDuration(initial, 1.5, max, 6); got != max {
		t.Fatalf("attempt6 = %s, want %s", got, max)
	}
}

type fakeRunner struct {
	fn func(args ...string) ([]byte, []byte, error)
}

func (f fakeRunner) Run(_ context.Context, args ...string) ([]byte, []byte, error) {
	return f.fn(args...)
}

func TestRediscoverPipelineRunSelectsParentCheckRun(t *testing.T) {
	runner := fakeRunner{fn: func(args ...string) ([]byte, []byte, error) {
		joined := strings.Join(args, " ")
		if joined == "api repos/TestGroup/catalog/commits/abc123/check-runs" {
			return []byte(`{"check_runs":[{"name":"Pipelines as Code CI / catalog-all-in-one-zlfkp-build-catalog-image","status":"completed","conclusion":"success","details_url":"https://x/workspace/devops~business-build~devops/pipeline/pipelineRuns/detail/catalog-all-in-one-zlfkp-build-catalog-image"},{"name":"Pipelines as Code CI / catalog-all-in-one","status":"completed","conclusion":"failure","details_url":"https://x/workspace/devops~business-build~devops/pipeline/pipelineRuns/detail/catalog-all-in-one-a1b2c"}]}`), nil, nil
		}
		return nil, []byte("unexpected args"), errors.New("unexpected")
	}}

	ghc := gh.NewClient("TestGroup", runner)
	ns, run, err := RediscoverPipelineRun(context.Background(), ghc, "catalog", "abc123", "catalog-all-in-one")
	if err != nil {
		t.Fatalf("RediscoverPipelineRun error: %v", err)
	}
	if ns != "devops" {
		t.Fatalf("namespace = %q, want devops", ns)
	}
	if run != "catalog-all-in-one-a1b2c" {
		t.Fatalf("run = %q, want catalog-all-in-one-a1b2c", run)
	}
}
