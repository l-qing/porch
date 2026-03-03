package integration

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"porch/pkg/component"
	"porch/pkg/config"
	"porch/pkg/gh"
	"porch/pkg/retrier"
	"porch/pkg/watcher"
)

type fakeRunner struct{}

func (fakeRunner) Run(_ context.Context, args ...string) ([]byte, []byte, error) {
	joined := strings.Join(args, " ")
	switch {
	case strings.Contains(joined, "commits/release-1.6"):
		return []byte(`{"sha":"abc123"}`), nil, nil
	case strings.Contains(joined, "commits/abc123/check-runs"):
		return []byte(`{"check_runs":[{"name":"Pipelines as Code CI / tp-all-in-one","status":"completed","conclusion":"failure","details_url":"https://edge.example.com/console-pipeline-v2/workspace/devops~business-build~devops/pipeline/pipelineRuns/detail/tp-all-in-one-abc12","external_id":"tp-all-in-one-abc12"}]}`), nil, nil
	default:
		return nil, []byte("unexpected fake gh call"), os.ErrNotExist
	}
}

func TestReplay_GHAndKubectl(t *testing.T) {
	ctx := context.Background()
	ghc := gh.NewClient("TestGroup", fakeRunner{})

	rc := config.RuntimeConfig{
		Components: []config.LoadedComponent{{
			Name:   "tektoncd-pipeline",
			Repo:   "tektoncd-pipeline",
			Branch: "release-1.6",
			Pipelines: []config.PipelineSpec{{
				Name: "tp-all-in-one",
			}},
		}},
	}

	components, err := component.Initialize(ctx, rc, ghc)
	if err != nil {
		t.Fatalf("Initialize error: %v", err)
	}
	if len(components) != 1 || len(components[0].Pipelines) != 1 {
		t.Fatalf("unexpected initialize result: %+v", components)
	}

	tmp := t.TempDir()
	kubectlPath := filepath.Join(tmp, "kubectl")
	script := "#!/bin/sh\n"
	if runtime.GOOS == "windows" {
		t.Skip("windows not supported in this integration test")
	}
	script += "echo '{\"status\":{\"conditions\":[{\"type\":\"Succeeded\",\"status\":\"True\",\"reason\":\"Completed\"}]}}'\n"
	if err := os.WriteFile(kubectlPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake kubectl: %v", err)
	}

	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)
	os.Setenv("PATH", tmp+string(os.PathListSeparator)+oldPath)

	pr := components[0].Pipelines[0]
	probe, err := watcher.ProbePipelineRun(ctx, pr.Namespace, pr.PipelineRun, "", "")
	if err != nil {
		t.Fatalf("ProbePipelineRun error: %v", err)
	}
	if probe.Status != "succeeded" {
		t.Fatalf("unexpected probe status: %+v", probe)
	}

	ns, run, err := retrier.RediscoverPipelineRun(ctx, ghc, "tektoncd-pipeline", "abc123", "tp-all-in-one")
	if err != nil {
		t.Fatalf("RediscoverPipelineRun error: %v", err)
	}
	if ns != "devops" || run != "tp-all-in-one-abc12" {
		t.Fatalf("unexpected rediscover result ns=%s run=%s", ns, run)
	}
}
