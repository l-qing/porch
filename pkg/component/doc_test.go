package component

import (
	"context"
	"strings"
	"testing"

	"porch/pkg/config"
	"porch/pkg/gh"
)

func TestParseDetailsURL(t *testing.T) {
	ns, run, note := ParseDetailsURL("https://edge.example.com/console-pipeline-v2/workspace/devops~business-build~devops/pipeline/pipelineRuns/detail/tp-all-in-one-2wn4t")
	if ns != "devops" || run != "tp-all-in-one-2wn4t" || note != "" {
		t.Fatalf("unexpected parse result ns=%q run=%q note=%q", ns, run, note)
	}
}

func TestParseDetailsURLMismatch(t *testing.T) {
	ns, run, note := ParseDetailsURL("https://edge.example.com/console-pipeline-v2/workspace/a~business-build~b/pipeline/pipelineRuns/detail/run-1")
	if ns != "" || run != "run-1" || note == "" {
		t.Fatalf("unexpected mismatch result ns=%q run=%q note=%q", ns, run, note)
	}
}

func TestParseDetailsURLForPipelineNormalizesRun(t *testing.T) {
	ns, run, note := ParseDetailsURLForPipeline("https://edge.example.com/console-pipeline-v2/workspace/devops~business-build~devops/pipeline/pipelineRuns/detail/tp-all-in-one-6c99n-build-nop-image", "tp-all-in-one")
	if ns != "devops" || run != "tp-all-in-one-6c99n" || note != "" {
		t.Fatalf("unexpected parse result ns=%q run=%q note=%q", ns, run, note)
	}
}

func TestNormalizePipelineRunName(t *testing.T) {
	got := NormalizePipelineRunName("to-all-in-one", "to-all-in-one-fsrv6-build-bundle-image-image")
	if got != "to-all-in-one-fsrv6" {
		t.Fatalf("got %q, want %q", got, "to-all-in-one-fsrv6")
	}

	got = NormalizePipelineRunName("tp-all-in-one", "tp-all-in-one-2wn4t")
	if got != "tp-all-in-one-2wn4t" {
		t.Fatalf("got %q, want unchanged", got)
	}
}

func TestFindPipelineCheckRunAvoidsChildMatch(t *testing.T) {
	runs := []gh.CheckRun{
		{Name: "Pipelines as Code CI / catalog-all-in-one-zlfkp-build-catalog-image", Status: "completed", Conclusion: "success"},
		{Name: "Pipelines as Code CI / catalog-all-in-one", Status: "completed", Conclusion: "failure"},
	}

	r, ok := FindPipelineCheckRun(runs, "catalog-all-in-one")
	if !ok {
		t.Fatal("expected to find exact check-run")
	}
	if LogicalCheckRunName(r.Name) != "catalog-all-in-one" {
		t.Fatalf("picked wrong run: %q", r.Name)
	}
	if r.Conclusion != "failure" {
		t.Fatalf("expected parent run conclusion failure, got %q", r.Conclusion)
	}
}

func TestPipelineRunFromCheckRun(t *testing.T) {
	run := PipelineRunFromCheckRun(gh.CheckRun{
		DetailsURL: "https://x/workspace/devops~business-build~devops/pipeline/pipelineRuns/detail/tp-all-in-one-4hqpn-build-nop-image",
	}, "tp-all-in-one")
	if run != "tp-all-in-one-4hqpn" {
		t.Fatalf("run = %q, want %q", run, "tp-all-in-one-4hqpn")
	}

	run = PipelineRunFromCheckRun(gh.CheckRun{
		ExternalID: "tp-all-in-one-4hqpn-build-nop-image",
	}, "tp-all-in-one")
	if run != "tp-all-in-one-4hqpn" {
		t.Fatalf("external run = %q, want %q", run, "tp-all-in-one-4hqpn")
	}
}

func TestFindPipelineCheckRunForRun(t *testing.T) {
	runs := []gh.CheckRun{
		{
			ID:         10,
			Name:       "Pipelines as Code CI / tp-all-in-one",
			Status:     "completed",
			Conclusion: "success",
			DetailsURL: "https://x/workspace/devops~business-build~devops/pipeline/pipelineRuns/detail/tp-all-in-one-old01",
		},
		{
			ID:         12,
			Name:       "Pipelines as Code CI / tp-all-in-one",
			Status:     "in_progress",
			Conclusion: "",
			DetailsURL: "https://x/workspace/devops~business-build~devops/pipeline/pipelineRuns/detail/tp-all-in-one-4hqpn",
		},
	}

	r, ok := FindPipelineCheckRunForRun(runs, "tp-all-in-one", "tp-all-in-one-4hqpn")
	if !ok {
		t.Fatal("expected to find matching run")
	}
	if r.ID != 12 {
		t.Fatalf("id = %d, want 12", r.ID)
	}

	_, ok = FindPipelineCheckRunForRun(runs, "tp-all-in-one", "tp-all-in-one-miss")
	if ok {
		t.Fatal("expected no match for run mismatch")
	}
}

func TestFindPipelineCheckRunForRunMatchesChildRunByNormalizedName(t *testing.T) {
	runs := []gh.CheckRun{
		{
			ID:         20,
			Name:       "Pipelines as Code CI / tp-all-in-one",
			Status:     "completed",
			Conclusion: "success",
			DetailsURL: "https://x/workspace/devops~business-build~devops/pipeline/pipelineRuns/detail/tp-all-in-one-gsl9t-build-events-image",
		},
	}

	r, ok := FindPipelineCheckRunForRun(runs, "tp-all-in-one", "tp-all-in-one-gsl9t")
	if !ok {
		t.Fatal("expected normalized match when check-run points to child PipelineRun")
	}
	if r.ID != 20 {
		t.Fatalf("id = %d, want 20", r.ID)
	}
}

func TestFindPipelineCheckRunPrefersNewestID(t *testing.T) {
	runs := []gh.CheckRun{
		{
			ID:         11,
			Name:       "Pipelines as Code CI / tp-all-in-one",
			Status:     "completed",
			Conclusion: "failure",
		},
		{
			ID:         15,
			Name:       "Pipelines as Code CI / tp-all-in-one",
			Status:     "in_progress",
			Conclusion: "",
		},
	}
	r, ok := FindPipelineCheckRun(runs, "tp-all-in-one")
	if !ok {
		t.Fatal("expected to find run")
	}
	if r.ID != 15 {
		t.Fatalf("id = %d, want 15", r.ID)
	}
}

type fakeRunner struct {
	fn func(args ...string) ([]byte, []byte, error)
}

func (f fakeRunner) Run(_ context.Context, args ...string) ([]byte, []byte, error) {
	return f.fn(args...)
}

func TestInitializeSelectsParentCheckRun(t *testing.T) {
	runner := fakeRunner{fn: func(args ...string) ([]byte, []byte, error) {
		joined := ""
		for i, a := range args {
			if i > 0 {
				joined += " "
			}
			joined += a
		}
		switch joined {
		case "api repos/TestGroup/catalog/commits/main":
			return []byte(`{"sha":"abc123"}`), nil, nil
		case "api repos/TestGroup/catalog/commits/abc123/check-runs":
			return []byte(`{"check_runs":[{"name":"Pipelines as Code CI / catalog-all-in-one-zlfkp-build-catalog-image","status":"completed","conclusion":"success","details_url":"https://x/workspace/devops~business-build~devops/pipeline/pipelineRuns/detail/catalog-all-in-one-zlfkp-build-catalog-image"},{"name":"Pipelines as Code CI / catalog-all-in-one","status":"completed","conclusion":"failure","details_url":"https://x/workspace/devops~business-build~devops/pipeline/pipelineRuns/detail/catalog-all-in-one-a1b2c"}]}`), nil, nil
		default:
			return nil, []byte("unexpected args"), context.Canceled
		}
	}}

	ghc := gh.NewClient("TestGroup", runner)
	rc := config.RuntimeConfig{
		Components: []config.LoadedComponent{{
			Name:   "catalog",
			Repo:   "catalog",
			Branch: "main",
			Pipelines: []config.PipelineSpec{{
				Name: "catalog-all-in-one",
			}},
		}},
	}

	components, err := Initialize(context.Background(), rc, ghc)
	if err != nil {
		t.Fatalf("Initialize error: %v", err)
	}
	if len(components) != 1 || len(components[0].Pipelines) != 1 {
		t.Fatalf("unexpected result: %+v", components)
	}
	p := components[0].Pipelines[0]
	if LogicalCheckRunName(p.CheckRunName) != "catalog-all-in-one" {
		t.Fatalf("picked wrong check-run: %q", p.CheckRunName)
	}
	if p.Conclusion != "failure" {
		t.Fatalf("expected parent conclusion failure, got %q", p.Conclusion)
	}
}

func TestInitializeUsesPullRequestHeadForPRMode(t *testing.T) {
	runner := fakeRunner{fn: func(args ...string) ([]byte, []byte, error) {
		joined := strings.Join(args, " ")
		switch joined {
		case "api repos/TestGroup/catalog/pulls/101":
			return []byte(`{"number":101,"state":"open","head":{"ref":"feat/add-golang-task","sha":"abc123"}}`), nil, nil
		case "api repos/TestGroup/catalog/commits/abc123/check-runs":
			return []byte(`{"check_runs":[{"name":"Pipelines as Code CI / catalog-all-in-one","status":"in_progress","conclusion":"","details_url":"https://x/workspace/devops~business-build~devops/pipeline/pipelineRuns/detail/catalog-all-in-one-a1b2c"}]}`), nil, nil
		default:
			return nil, []byte("unexpected args"), context.Canceled
		}
	}}

	ghc := gh.NewClient("TestGroup", runner)
	rc := config.RuntimeConfig{
		Components: []config.LoadedComponent{{
			Name:     "catalog#101",
			Repo:     "catalog",
			PRNumber: 101,
			Pipelines: []config.PipelineSpec{{
				Name: "catalog-all-in-one",
			}},
		}},
	}

	components, err := Initialize(context.Background(), rc, ghc)
	if err != nil {
		t.Fatalf("Initialize error: %v", err)
	}
	if len(components) != 1 {
		t.Fatalf("components len=%d, want 1", len(components))
	}
	if components[0].Branch != "feat/add-golang-task" {
		t.Fatalf("branch=%q, want feat/add-golang-task", components[0].Branch)
	}
	if components[0].SHA != "abc123" {
		t.Fatalf("sha=%q, want abc123", components[0].SHA)
	}
}
