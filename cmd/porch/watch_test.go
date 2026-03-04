package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"porch/pkg/config"
	"porch/pkg/gh"
	pipestatus "porch/pkg/pipeline"
	"porch/pkg/resolver"

	"github.com/sirupsen/logrus"
)

type fakeRunner struct {
	fn func(args ...string) ([]byte, []byte, error)
}

func (f fakeRunner) Run(_ context.Context, args ...string) ([]byte, []byte, error) {
	return f.fn(args...)
}

func TestFallbackProbeFromGHSelectsParentCheckRun(t *testing.T) {
	runner := fakeRunner{fn: func(args ...string) ([]byte, []byte, error) {
		joined := strings.Join(args, " ")
		switch joined {
		case "api repos/TestGroup/catalog/commits/abc123/check-runs":
			return []byte(`{"check_runs":[{"name":"Pipelines as Code CI / catalog-all-in-one-zlfkp-build-catalog-image","status":"completed","conclusion":"success","details_url":"https://x/workspace/devops~business-build~devops/pipeline/pipelineRuns/detail/catalog-all-in-one-zlfkp-build-catalog-image"},{"name":"Pipelines as Code CI / catalog-all-in-one","status":"completed","conclusion":"failure","details_url":"https://x/workspace/devops~business-build~devops/pipeline/pipelineRuns/detail/catalog-all-in-one-a1b2c"}]}`), nil, nil
		default:
			return nil, []byte("unexpected args"), errors.New("unexpected")
		}
	}}

	ghc := gh.NewClient("TestGroup", runner)
	c := &trackedComponent{Name: "catalog", Repo: "catalog", Branch: "main", SHA: "abc123"}

	res, source, err := fallbackProbeFromGH(context.Background(), ghc, c, "catalog-all-in-one", "")
	if err != nil {
		t.Fatalf("fallbackProbeFromGH error: %v", err)
	}
	if source != "gh_current_sha" {
		t.Fatalf("source = %q, want gh_current_sha", source)
	}
	if res.Status != pipestatus.StatusFailed || res.Conclusion != pipestatus.ConclusionFailure {
		t.Fatalf("unexpected fallback probe result: %+v", res)
	}
}

func TestFallbackProbeFromGHMatchesFullRun(t *testing.T) {
	runner := fakeRunner{fn: func(args ...string) ([]byte, []byte, error) {
		joined := strings.Join(args, " ")
		switch joined {
		case "api repos/TestGroup/tektoncd-pipeline/commits/abc123/check-runs":
			return []byte(`{"check_runs":[{"id":20,"name":"Pipelines as Code CI / tp-all-in-one","status":"completed","conclusion":"success","details_url":"https://x/workspace/devops~business-build~devops/pipeline/pipelineRuns/detail/tp-all-in-one-gsl9t-build-events-image"}]}`), nil, nil
		default:
			return nil, []byte("unexpected args"), errors.New("unexpected")
		}
	}}

	ghc := gh.NewClient("TestGroup", runner)
	c := &trackedComponent{Name: "tektoncd-pipeline", Repo: "tektoncd-pipeline", Branch: "release-1.0", SHA: "abc123"}

	res, source, err := fallbackProbeFromGH(context.Background(), ghc, c, "tp-all-in-one", "tp-all-in-one-gsl9t-build-events-image")
	if err != nil {
		t.Fatalf("fallbackProbeFromGH error: %v", err)
	}
	if source != "gh_current_sha" {
		t.Fatalf("source = %q, want gh_current_sha", source)
	}
	if res.Status != pipestatus.StatusSucceeded || res.Conclusion != pipestatus.ConclusionSuccess || res.Reason != "gh_fallback" {
		t.Fatalf("unexpected fallback probe result: %+v", res)
	}
}

func TestFallbackProbeFromGHRunMismatchReturnsRunning(t *testing.T) {
	runner := fakeRunner{fn: func(args ...string) ([]byte, []byte, error) {
		joined := strings.Join(args, " ")
		switch joined {
		case "api repos/TestGroup/tektoncd-pipeline/commits/abc123/check-runs":
			return []byte(`{"check_runs":[{"id":21,"name":"Pipelines as Code CI / tp-all-in-one","status":"completed","conclusion":"success","details_url":"https://x/workspace/devops~business-build~devops/pipeline/pipelineRuns/detail/tp-all-in-one-xxxx1-build-events-image"}]}`), nil, nil
		default:
			return nil, []byte("unexpected args"), errors.New("unexpected")
		}
	}}

	ghc := gh.NewClient("TestGroup", runner)
	c := &trackedComponent{Name: "tektoncd-pipeline", Repo: "tektoncd-pipeline", Branch: "release-1.0", SHA: "abc123"}

	res, source, err := fallbackProbeFromGH(context.Background(), ghc, c, "tp-all-in-one", "tp-all-in-one-gsl9t")
	if err != nil {
		t.Fatalf("fallbackProbeFromGH error: %v", err)
	}
	if source != "gh_current_sha" {
		t.Fatalf("source = %q, want gh_current_sha", source)
	}
	if res.Status != pipestatus.StatusRunning || res.Conclusion != pipestatus.ConclusionUnknown || res.Reason != "gh_fallback_run_mismatch" {
		t.Fatalf("unexpected fallback probe result: %+v", res)
	}
}

func TestFallbackProbeFromGHUsesFailureAnnotations(t *testing.T) {
	runner := fakeRunner{fn: func(args ...string) ([]byte, []byte, error) {
		joined := strings.Join(args, " ")
		switch joined {
		case "api repos/TestGroup/tektoncd-pipeline/commits/abc123/check-runs":
			return []byte(`{"check_runs":[{"id":22,"name":"Pipelines as Code CI / tp-all-in-one","status":"completed","conclusion":"success","details_url":"https://x/workspace/devops~business-build~devops/pipeline/pipelineRuns/detail/tp-all-in-one-gsl9t-build-events-image","output":{"annotations_count":1}}]}`), nil, nil
		case "api repos/TestGroup/tektoncd-pipeline/check-runs/22/annotations?per_page=100":
			return []byte(`[{"annotation_level":"failure","title":"2026-03-02T16#L41","message":"scan failed"}]`), nil, nil
		default:
			return nil, []byte("unexpected args"), errors.New("unexpected")
		}
	}}

	ghc := gh.NewClient("TestGroup", runner)
	c := &trackedComponent{Name: "tektoncd-pipeline", Repo: "tektoncd-pipeline", Branch: "release-1.0", SHA: "abc123"}

	res, source, err := fallbackProbeFromGH(context.Background(), ghc, c, "tp-all-in-one", "tp-all-in-one-gsl9t")
	if err != nil {
		t.Fatalf("fallbackProbeFromGH error: %v", err)
	}
	if source != "gh_current_sha" {
		t.Fatalf("source = %q, want gh_current_sha", source)
	}
	if res.Status != pipestatus.StatusFailed || res.Conclusion != pipestatus.ConclusionFailure || res.Reason != "gh_fallback_annotation_failure" {
		t.Fatalf("unexpected fallback probe result: %+v", res)
	}
}

func TestResolveFinalBranchPriority(t *testing.T) {
	tracked := map[string]*trackedComponent{
		"tektoncd-pipeline": {Branch: "release-1.6"},
	}

	cfg := config.RuntimeConfig{Root: config.Root{FinalAction: config.FinalAction{
		Branch:              "main",
		BranchFromComponent: "tektoncd-pipeline",
	}}}

	branch, err := resolveFinalBranch("release-2.0", cfg, tracked)
	if err != nil {
		t.Fatalf("resolveFinalBranch error: %v", err)
	}
	if branch != "release-2.0" {
		t.Fatalf("branch = %q, want release-2.0", branch)
	}

	branch, err = resolveFinalBranch("", cfg, tracked)
	if err != nil {
		t.Fatalf("resolveFinalBranch error: %v", err)
	}
	if branch != "main" {
		t.Fatalf("branch = %q, want main", branch)
	}

	cfg.Root.FinalAction.Branch = ""
	branch, err = resolveFinalBranch("", cfg, tracked)
	if err != nil {
		t.Fatalf("resolveFinalBranch error: %v", err)
	}
	if branch != "release-1.6" {
		t.Fatalf("branch = %q, want release-1.6", branch)
	}
}

func TestCommitChecksURL(t *testing.T) {
	got := commitChecksURL("TestGroup", "catalog", "abc123")
	want := "https://github.com/TestGroup/catalog/commit/abc123/checks"
	if got != want {
		t.Fatalf("url = %q, want %q", got, want)
	}
}

func TestCommitChecksURLMissingParts(t *testing.T) {
	got := commitChecksURL("TestGroup", "catalog", "")
	if got != "" {
		t.Fatalf("url = %q, want empty", got)
	}
}

func TestGHFallbackEventMessage(t *testing.T) {
	got := ghFallbackEventMessage("TestGroup", "catalog", "abc123")
	want := "kubectl probe failed, fallback to GH check-run: https://github.com/TestGroup/catalog/commit/abc123/checks"
	if got != want {
		t.Fatalf("message = %q, want %q", got, want)
	}
}

func TestWatchOnceGHOnlySkipsKubectl(t *testing.T) {
	tmp := t.TempDir()
	kubectlPath := filepath.Join(tmp, "kubectl")
	markerPath := filepath.Join(tmp, "kubectl-called")
	script := "#!/bin/sh\n" +
		"echo called > \"" + markerPath + "\"\n" +
		"exit 42\n"
	if err := os.WriteFile(kubectlPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake kubectl: %v", err)
	}

	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)
	os.Setenv("PATH", tmp+string(os.PathListSeparator)+oldPath)

	runner := fakeRunner{fn: func(args ...string) ([]byte, []byte, error) {
		joined := strings.Join(args, " ")
		switch joined {
		case "api repos/TestGroup/catalog/commits/abc123/check-runs":
			return []byte(`{"check_runs":[{"id":30,"name":"Pipelines as Code CI / catalog-all-in-one","status":"completed","conclusion":"success","details_url":"https://x/workspace/devops~business-build~devops/pipeline/pipelineRuns/detail/catalog-all-in-one-abc12"}]}`), nil, nil
		default:
			return nil, []byte("unexpected args"), errors.New("unexpected")
		}
	}}

	ghc := gh.NewClient("TestGroup", runner)
	cfg := config.RuntimeConfig{
		Root: config.Root{
			Connection: config.Connection{
				GitHubOrg: "TestGroup",
			},
		},
	}
	dag, err := resolver.New([]config.LoadedComponent{{Name: "catalog"}}, map[string]config.Depends{})
	if err != nil {
		t.Fatalf("build dag: %v", err)
	}
	tracked := map[string]*trackedComponent{
		"catalog": {
			Name:   "catalog",
			Repo:   "catalog",
			Branch: "main",
			SHA:    "abc123",
			Pipelines: map[string]*trackedPipeline{
				"catalog-all-in-one": {
					Name:        "catalog-all-in-one",
					Namespace:   "devops",
					PipelineRun: "catalog-all-in-one-abc12",
					Status:      pipestatus.StatusWatching,
				},
			},
		},
	}
	emit := func(watchEventKind, string, logrus.Fields) {}
	deps := watchOnceDeps{
		log:    logrus.New(),
		cfg:    cfg,
		ghc:    ghc,
		dag:    dag,
		mode:   probeModeGHOnly,
		dryRun: true,
		emit:   emit,
	}

	if err := watchOnce(context.Background(), tracked, deps); err != nil {
		t.Fatalf("watchOnce error: %v", err)
	}

	gotStatus := tracked["catalog"].Pipelines["catalog-all-in-one"].Status
	if gotStatus != pipestatus.StatusSucceeded {
		t.Fatalf("status = %q, want succeeded", gotStatus)
	}

	if _, err := os.Stat(markerPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("kubectl should not be called in gh-only mode")
	}
}

func TestWatchOnceStopsImmediatelyWhenStopRequested(t *testing.T) {
	stop := false
	calls := 0
	runner := fakeRunner{fn: func(args ...string) ([]byte, []byte, error) {
		calls++
		stop = true
		return nil, []byte("signal: interrupt"), errors.New("exit status 1")
	}}

	ghc := gh.NewClient("TestGroup", runner)
	cfg := config.RuntimeConfig{
		Root: config.Root{
			Connection: config.Connection{
				GitHubOrg: "TestGroup",
			},
		},
	}
	dag, err := resolver.New([]config.LoadedComponent{{Name: "catalog"}}, map[string]config.Depends{})
	if err != nil {
		t.Fatalf("build dag: %v", err)
	}
	tracked := map[string]*trackedComponent{
		"catalog": {
			Name:   "catalog",
			Repo:   "catalog",
			Branch: "main",
			SHA:    "abc123",
			Pipelines: map[string]*trackedPipeline{
				"catalog-all-in-one": {
					Name:        "catalog-all-in-one",
					Namespace:   "devops",
					PipelineRun: "catalog-all-in-one-abc12",
					Status:      pipestatus.StatusWatching,
				},
			},
		},
	}
	queryWarnCount := 0
	emit := func(kind watchEventKind, _ string, _ logrus.Fields) {
		if kind == eventQueryWarn {
			queryWarnCount++
		}
	}
	shouldStop := func() bool {
		return stop
	}
	deps := watchOnceDeps{
		log:        logrus.New(),
		cfg:        cfg,
		ghc:        ghc,
		dag:        dag,
		mode:       probeModeGHOnly,
		dryRun:     true,
		emit:       emit,
		shouldStop: shouldStop,
	}

	err = watchOnce(context.Background(), tracked, deps)
	if !errors.Is(err, errWatchStopRequested) {
		t.Fatalf("error = %v, want errWatchStopRequested", err)
	}
	if calls != 1 {
		t.Fatalf("gh calls = %d, want 1", calls)
	}
	if queryWarnCount != 0 {
		t.Fatalf("query warn count = %d, want 0", queryWarnCount)
	}
}

func TestWatchOnceExhaustedNotificationOnlyOnce(t *testing.T) {
	runner := fakeRunner{fn: func(args ...string) ([]byte, []byte, error) {
		joined := strings.Join(args, " ")
		switch joined {
		case "api repos/TestGroup/tektoncd-operator/commits/abc123/check-runs":
			return []byte(`{"check_runs":[{"id":40,"name":"Pipelines as Code CI / to-all-in-one","status":"completed","conclusion":"failure","details_url":"https://x/workspace/devops~business-build~devops/pipeline/pipelineRuns/detail/to-all-in-one-abc12"}]}`), nil, nil
		default:
			return nil, []byte("unexpected args"), errors.New("unexpected")
		}
	}}

	ghc := gh.NewClient("TestGroup", runner)
	cfg := config.RuntimeConfig{
		Root: config.Root{
			Connection: config.Connection{
				GitHubOrg: "TestGroup",
			},
			Retry: config.Retry{
				MaxRetries: 1,
				Backoff: config.Backoff{
					Initial:    config.Duration{Duration: time.Second},
					Multiplier: 1,
					Max:        config.Duration{Duration: time.Second},
				},
			},
		},
	}
	dag, err := resolver.New([]config.LoadedComponent{{Name: "tektoncd-operator"}}, map[string]config.Depends{})
	if err != nil {
		t.Fatalf("build dag: %v", err)
	}
	tracked := map[string]*trackedComponent{
		"tektoncd-operator": {
			Name:   "tektoncd-operator",
			Repo:   "tektoncd-operator",
			Branch: "release-4.6",
			SHA:    "abc123",
			Pipelines: map[string]*trackedPipeline{
				"to-all-in-one": {
					Name:       "to-all-in-one",
					Status:     pipestatus.StatusWatching,
					RetryCount: 1,
				},
			},
		},
	}
	exhaustedCount := 0
	emit := func(kind watchEventKind, _ string, _ logrus.Fields) {
		if kind == eventExhausted {
			exhaustedCount++
		}
	}
	deps := watchOnceDeps{
		log:    logrus.New(),
		cfg:    cfg,
		ghc:    ghc,
		dag:    dag,
		mode:   probeModeGHOnly,
		dryRun: true,
		emit:   emit,
	}

	for i := 0; i < 2; i++ {
		if err := watchOnce(context.Background(), tracked, deps); err != nil {
			t.Fatalf("watchOnce error: %v", err)
		}
	}

	if exhaustedCount != 1 {
		t.Fatalf("exhausted notifications = %d, want 1", exhaustedCount)
	}
	if got := tracked["tektoncd-operator"].Pipelines["to-all-in-one"].Status; got != pipestatus.StatusExhausted {
		t.Fatalf("status = %q, want exhausted", got)
	}
}

func TestWatchOnceMaxRetriesZeroDisablesExhaustedThreshold(t *testing.T) {
	runner := fakeRunner{fn: func(args ...string) ([]byte, []byte, error) {
		joined := strings.Join(args, " ")
		switch joined {
		case "api repos/TestGroup/tektoncd-operator/commits/abc123/check-runs":
			return []byte(`{"check_runs":[{"id":41,"name":"Pipelines as Code CI / to-all-in-one","status":"completed","conclusion":"failure","details_url":"https://x/workspace/devops~business-build~devops/pipeline/pipelineRuns/detail/to-all-in-one-abc13"}]}`), nil, nil
		default:
			return nil, []byte("unexpected args"), errors.New("unexpected")
		}
	}}

	ghc := gh.NewClient("TestGroup", runner)
	cfg := config.RuntimeConfig{
		Root: config.Root{
			Connection: config.Connection{
				GitHubOrg: "TestGroup",
			},
			Retry: config.Retry{
				MaxRetries: 0,
				Backoff: config.Backoff{
					Initial:    config.Duration{Duration: time.Second},
					Multiplier: 1,
					Max:        config.Duration{Duration: time.Second},
				},
			},
		},
	}
	dag, err := resolver.New([]config.LoadedComponent{{Name: "tektoncd-operator"}}, map[string]config.Depends{})
	if err != nil {
		t.Fatalf("build dag: %v", err)
	}
	tracked := map[string]*trackedComponent{
		"tektoncd-operator": {
			Name:   "tektoncd-operator",
			Repo:   "tektoncd-operator",
			Branch: "release-4.6",
			SHA:    "abc123",
			Pipelines: map[string]*trackedPipeline{
				"to-all-in-one": {
					Name:       "to-all-in-one",
					Status:     pipestatus.StatusWatching,
					RetryCount: 7,
				},
			},
		},
	}
	exhaustedCount := 0
	emit := func(kind watchEventKind, _ string, _ logrus.Fields) {
		if kind == eventExhausted {
			exhaustedCount++
		}
	}
	deps := watchOnceDeps{
		log:    logrus.New(),
		cfg:    cfg,
		ghc:    ghc,
		dag:    dag,
		mode:   probeModeGHOnly,
		dryRun: true,
		emit:   emit,
	}

	if err := watchOnce(context.Background(), tracked, deps); err != nil {
		t.Fatalf("watchOnce error: %v", err)
	}

	p := tracked["tektoncd-operator"].Pipelines["to-all-in-one"]
	if p.Status != pipestatus.StatusBackoff {
		t.Fatalf("status = %q, want backoff", p.Status)
	}
	if p.RetryAfter == nil {
		t.Fatalf("retryAfter should be set when max_retries is 0")
	}
	if exhaustedCount != 0 {
		t.Fatalf("exhausted notifications = %d, want 0", exhaustedCount)
	}
}

func TestScopedSuccessBranch(t *testing.T) {
	tracked := map[string]*trackedComponent{
		"z-comp": {Branch: "release-z"},
		"a-comp": {Branch: "release-a"},
	}

	got := scopedSuccessBranch(tracked)
	if got != "release-a" {
		t.Fatalf("branch = %q, want release-a", got)
	}

	got = scopedSuccessBranch(map[string]*trackedComponent{"x": {Branch: ""}})
	if got != "scoped" {
		t.Fatalf("branch = %q, want scoped", got)
	}
}

func TestSuccessSummaryBranch(t *testing.T) {
	tracked := map[string]*trackedComponent{
		"z-comp": {Branch: "release-z"},
		"a-comp": {Branch: "release-a"},
	}

	scoped := successSummaryBranch(true, "", config.RuntimeConfig{}, tracked, false)
	if scoped != "release-a" {
		t.Fatalf("scoped branch = %q, want release-a", scoped)
	}

	disabled := successSummaryBranch(false, "", config.RuntimeConfig{}, tracked, false)
	if disabled != "multi-branch" {
		t.Fatalf("disabled branch = %q, want multi-branch", disabled)
	}

	enabled := successSummaryBranch(false, "release-1.8", config.RuntimeConfig{}, tracked, true)
	if enabled != "release-1.8" {
		t.Fatalf("enabled branch = %q, want release-1.8", enabled)
	}
}

func TestExpandRuntimeDependencies(t *testing.T) {
	components := []config.LoadedComponent{
		{Name: "pipeline@main", Branch: "main"},
		{Name: "pipeline@release-1.8", Branch: "release-1.8"},
		{Name: "catalog", Branch: "release-4.2"},
	}
	raw := map[string]config.Depends{
		"catalog": {DependsOn: []string{"pipeline"}},
	}

	got := expandRuntimeDependencies(components, raw)
	deps := got["catalog"].DependsOn
	if len(deps) != 2 {
		t.Fatalf("catalog deps len=%d, want 2", len(deps))
	}
	set := map[string]struct{}{}
	for _, d := range deps {
		set[d] = struct{}{}
	}
	if _, ok := set["pipeline@main"]; !ok {
		t.Fatalf("missing dep pipeline@main")
	}
	if _, ok := set["pipeline@release-1.8"]; !ok {
		t.Fatalf("missing dep pipeline@release-1.8")
	}
}

func TestApplyWatchScopeByBaseNameAndBranch(t *testing.T) {
	cfg := config.RuntimeConfig{
		Components: []config.LoadedComponent{
			{Name: "tektoncd-pipeline@main", Repo: "tektoncd-pipeline", Branch: "main", Pipelines: []config.PipelineSpec{{Name: "tp-all-in-one"}}},
			{Name: "tektoncd-pipeline@release-1.8", Repo: "tektoncd-pipeline", Branch: "release-1.8", Pipelines: []config.PipelineSpec{{Name: "tp-all-in-one"}}},
		},
	}
	opts := watchOptions{
		componentName: "tektoncd-pipeline",
		branch:        "release-1.8",
	}

	scoped, err := applyWatchScope(&cfg, opts)
	if err != nil {
		t.Fatalf("applyWatchScope error: %v", err)
	}
	if !scoped {
		t.Fatalf("expect scoped=true")
	}
	if len(cfg.Components) != 1 {
		t.Fatalf("components len=%d, want 1", len(cfg.Components))
	}
	if cfg.Components[0].Name != "tektoncd-pipeline@release-1.8" {
		t.Fatalf("selected component=%q", cfg.Components[0].Name)
	}
}

func TestShouldSkipPatternResolutionForAdHocScope(t *testing.T) {
	components := []config.LoadedComponent{
		{Name: "tektoncd-pipeline", Repo: "tektoncd-pipeline", BranchPatterns: []string{"^main$"}, Pipelines: []config.PipelineSpec{{Name: "tp-all-in-one"}}},
		{Name: "tektoncd-hub", Repo: "tektoncd-hub", Branch: "main", Pipelines: []config.PipelineSpec{{Name: "th-all-in-one"}}},
	}

	if !shouldSkipPatternResolutionForAdHocScope(components, watchOptions{
		componentName: "tektoncd-operator",
		pipelineName:  "to-all-in-one",
	}) {
		t.Fatalf("expect skip=true for ad-hoc scoped watch")
	}

	if shouldSkipPatternResolutionForAdHocScope(components, watchOptions{
		componentName: "tektoncd-pipeline",
		pipelineName:  "tp-all-in-one",
	}) {
		t.Fatalf("expect skip=false when component exists in config")
	}

	if shouldSkipPatternResolutionForAdHocScope(components, watchOptions{
		componentName: "tektoncd-operator",
	}) {
		t.Fatalf("expect skip=false when pipeline is empty")
	}
}

func TestApplyWatchScopeBuildsAdHocComponentWhenMissingInConfig(t *testing.T) {
	cfg := config.RuntimeConfig{
		Components: []config.LoadedComponent{
			{Name: "tektoncd-pipeline", Repo: "tektoncd-pipeline", Branch: "main", Pipelines: []config.PipelineSpec{{Name: "tp-all-in-one"}}},
		},
	}
	opts := watchOptions{
		componentName: "tektoncd-operator",
		pipelineName:  "to-all-in-one",
		branch:        "main",
	}

	scoped, err := applyWatchScope(&cfg, opts)
	if err != nil {
		t.Fatalf("applyWatchScope error: %v", err)
	}
	if !scoped {
		t.Fatalf("expect scoped=true")
	}
	if len(cfg.Components) != 1 {
		t.Fatalf("components len=%d, want 1", len(cfg.Components))
	}
	c := cfg.Components[0]
	if c.Name != "tektoncd-operator" || c.Repo != "tektoncd-operator" || c.Branch != "main" {
		t.Fatalf("unexpected ad-hoc component: %+v", c)
	}
	if len(c.Pipelines) != 1 {
		t.Fatalf("pipelines len=%d, want 1", len(c.Pipelines))
	}
	if c.Pipelines[0].Name != "to-all-in-one" {
		t.Fatalf("pipeline name=%q, want to-all-in-one", c.Pipelines[0].Name)
	}
	if c.Pipelines[0].RetryCommand != "/test to-all-in-one branch:{branch}" {
		t.Fatalf("retry command=%q", c.Pipelines[0].RetryCommand)
	}
}

func TestApplyWatchScopeByBaseNameAndBranchPattern(t *testing.T) {
	cfg := config.RuntimeConfig{
		Components: []config.LoadedComponent{
			{Name: "tektoncd-pipeline@main", Repo: "tektoncd-pipeline", Branch: "main", Pipelines: []config.PipelineSpec{{Name: "tp-all-in-one"}}},
			{Name: "tektoncd-pipeline@release-1.8", Repo: "tektoncd-pipeline", Branch: "release-1.8", Pipelines: []config.PipelineSpec{{Name: "tp-all-in-one"}}},
			{Name: "tektoncd-pipeline@release-1.9", Repo: "tektoncd-pipeline", Branch: "release-1.9", Pipelines: []config.PipelineSpec{{Name: "tp-all-in-one"}}},
		},
	}
	opts := watchOptions{
		componentName: "tektoncd-pipeline",
		branchPattern: "^release-1\\.[89]$",
	}

	scoped, err := applyWatchScope(&cfg, opts)
	if err != nil {
		t.Fatalf("applyWatchScope error: %v", err)
	}
	if !scoped {
		t.Fatalf("expect scoped=true")
	}
	if len(cfg.Components) != 2 {
		t.Fatalf("components len=%d, want 2", len(cfg.Components))
	}
	got := map[string]struct{}{}
	for _, c := range cfg.Components {
		got[c.Name] = struct{}{}
	}
	for _, want := range []string{"tektoncd-pipeline@release-1.8", "tektoncd-pipeline@release-1.9"} {
		if _, ok := got[want]; !ok {
			t.Fatalf("missing selected component=%q, got=%v", want, got)
		}
	}
}

func TestApplyWatchScopeRejectsBranchAndBranchPatternTogether(t *testing.T) {
	cfg := config.RuntimeConfig{
		Components: []config.LoadedComponent{
			{Name: "tektoncd-pipeline", Repo: "tektoncd-pipeline", Branch: "main", Pipelines: []config.PipelineSpec{{Name: "tp-all-in-one"}}},
		},
	}
	_, err := applyWatchScope(&cfg, watchOptions{
		componentName: "tektoncd-pipeline",
		branch:        "main",
		branchPattern: "^main$",
	})
	if err == nil || err.Error() != "--branch and --branch-pattern are mutually exclusive" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestApplyWatchScopeRejectsBranchPatternWithoutComponent(t *testing.T) {
	cfg := config.RuntimeConfig{}
	_, err := applyWatchScope(&cfg, watchOptions{
		branchPattern: "^release-.*$",
	})
	if err == nil || err.Error() != "--branch-pattern requires --component" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestApplyWatchScopeRejectsInvalidBranchPattern(t *testing.T) {
	cfg := config.RuntimeConfig{
		Components: []config.LoadedComponent{
			{Name: "tektoncd-pipeline", Repo: "tektoncd-pipeline", Branch: "main", Pipelines: []config.PipelineSpec{{Name: "tp-all-in-one"}}},
		},
	}
	_, err := applyWatchScope(&cfg, watchOptions{
		componentName: "tektoncd-pipeline",
		branchPattern: "[",
	})
	if err == nil || !strings.Contains(err.Error(), "compile --branch-pattern") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestApplyWatchScopeBuildsAdHocComponentsByBranchPatternWhenMissingInConfig(t *testing.T) {
	cfg := config.RuntimeConfig{
		Components: []config.LoadedComponent{
			{Name: "tektoncd-pipeline", Repo: "tektoncd-pipeline", Branch: "main", Pipelines: []config.PipelineSpec{{Name: "tp-all-in-one"}}},
		},
	}
	ghc := gh.NewClient("TestGroup", fakeRunner{fn: func(args ...string) ([]byte, []byte, error) {
		joined := strings.Join(args, " ")
		if joined != "api --paginate repos/TestGroup/tektoncd-operator/branches?per_page=100 --jq .[].name" {
			return nil, []byte("unexpected args"), errors.New("unexpected")
		}
		return []byte("release-1.9\nfeature-x\nmain\n"), nil, nil
	}})
	scoped, err := applyWatchScopeWithBranchLister(context.Background(), &cfg, watchOptions{
		componentName: "tektoncd-operator",
		pipelineName:  "to-all-in-one",
		branchPattern: "^(main|release-.*)$",
	}, ghc)
	if err != nil {
		t.Fatalf("applyWatchScopeWithBranchLister error: %v", err)
	}
	if !scoped {
		t.Fatalf("expect scoped=true")
	}
	if len(cfg.Components) != 2 {
		t.Fatalf("components len=%d, want 2", len(cfg.Components))
	}
	if cfg.Components[0].Name != "tektoncd-operator@main" || cfg.Components[0].Branch != "main" {
		t.Fatalf("unexpected first ad-hoc component: %+v", cfg.Components[0])
	}
	if cfg.Components[1].Name != "tektoncd-operator@release-1.9" || cfg.Components[1].Branch != "release-1.9" {
		t.Fatalf("unexpected second ad-hoc component: %+v", cfg.Components[1])
	}
	if got := cfg.Components[0].Pipelines[0].RetryCommand; got != "/test to-all-in-one branch:{branch}" {
		t.Fatalf("retry command=%q", got)
	}
}

func TestApplyWatchScopeRejectsAdHocBranchPatternWithoutLister(t *testing.T) {
	cfg := config.RuntimeConfig{
		Components: []config.LoadedComponent{
			{Name: "tektoncd-pipeline", Repo: "tektoncd-pipeline", Branch: "main", Pipelines: []config.PipelineSpec{{Name: "tp-all-in-one"}}},
		},
	}
	_, err := applyWatchScopeWithBranchLister(context.Background(), &cfg, watchOptions{
		componentName: "tektoncd-operator",
		pipelineName:  "to-all-in-one",
		branchPattern: "^(main|release-.*)$",
	}, nil)
	if err == nil || err.Error() != "--branch-pattern requires branch lister when component \"tektoncd-operator\" is not defined in config" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestApplyWatchScopeRejectsAdHocBranchPatternWhenNoBranchMatched(t *testing.T) {
	cfg := config.RuntimeConfig{
		Components: []config.LoadedComponent{
			{Name: "tektoncd-pipeline", Repo: "tektoncd-pipeline", Branch: "main", Pipelines: []config.PipelineSpec{{Name: "tp-all-in-one"}}},
		},
	}
	ghc := gh.NewClient("TestGroup", fakeRunner{fn: func(args ...string) ([]byte, []byte, error) {
		return []byte("main\nrelease-1.9\n"), nil, nil
	}})
	_, err := applyWatchScopeWithBranchLister(context.Background(), &cfg, watchOptions{
		componentName: "tektoncd-operator",
		pipelineName:  "to-all-in-one",
		branchPattern: "^release-2\\..*$",
	}, ghc)
	if err == nil || err.Error() != "branch pattern \"^release-2\\\\..*$\" matched no branches under component \"tektoncd-operator\"" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestApplyWatchScopeRejectsAdHocBranchPatternWhenListBranchesFailed(t *testing.T) {
	cfg := config.RuntimeConfig{
		Components: []config.LoadedComponent{
			{Name: "tektoncd-pipeline", Repo: "tektoncd-pipeline", Branch: "main", Pipelines: []config.PipelineSpec{{Name: "tp-all-in-one"}}},
		},
	}
	ghc := gh.NewClient("TestGroup", fakeRunner{fn: func(args ...string) ([]byte, []byte, error) {
		return nil, []byte("boom"), errors.New("list failed")
	}})
	_, err := applyWatchScopeWithBranchLister(context.Background(), &cfg, watchOptions{
		componentName: "tektoncd-operator",
		pipelineName:  "to-all-in-one",
		branchPattern: "^(main|release-.*)$",
	}, ghc)
	if err == nil || !strings.Contains(err.Error(), "list branches for tektoncd-operator") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestApplyWatchScopeRejectsBranchPatternWithoutPipelineForMissingComponent(t *testing.T) {
	cfg := config.RuntimeConfig{
		Components: []config.LoadedComponent{
			{Name: "tektoncd-pipeline", Repo: "tektoncd-pipeline", Branch: "main", Pipelines: []config.PipelineSpec{{Name: "tp-all-in-one"}}},
		},
	}
	_, err := applyWatchScopeWithBranchLister(context.Background(), &cfg, watchOptions{
		componentName: "tektoncd-operator",
		branchPattern: "^(main|release-.*)$",
	}, nil)
	if err == nil || err.Error() != "component \"tektoncd-operator\" not found" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestApplyWatchScopeRejectsBranchPatternForMatchedComponentWhenNoBranchMatched(t *testing.T) {
	cfg := config.RuntimeConfig{
		Components: []config.LoadedComponent{
			{Name: "tektoncd-operator@main", Repo: "tektoncd-operator", Branch: "main", Pipelines: []config.PipelineSpec{{Name: "to-all-in-one"}}},
			{Name: "tektoncd-operator@release-1.9", Repo: "tektoncd-operator", Branch: "release-1.9", Pipelines: []config.PipelineSpec{{Name: "to-all-in-one"}}},
		},
	}
	_, err := applyWatchScope(&cfg, watchOptions{
		componentName: "tektoncd-operator",
		branchPattern: "^release-2\\..*$",
	})
	if err == nil || err.Error() != "branch pattern \"^release-2\\\\..*$\" matched no branches under component \"tektoncd-operator\"" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolvePatternComponentsSkipsBranchListWhenNoPattern(t *testing.T) {
	called := false
	ghc := gh.NewClient("TestGroup", fakeRunner{fn: func(args ...string) ([]byte, []byte, error) {
		called = true
		return nil, []byte("unexpected call"), errors.New("unexpected")
	}})
	cfg := config.RuntimeConfig{
		Components: []config.LoadedComponent{
			{Name: "tektoncd-pipeline", Repo: "tektoncd-pipeline", Branch: "main", Pipelines: []config.PipelineSpec{{Name: "tp-all-in-one"}}},
		},
	}

	resolved, err := resolvePatternComponents(context.Background(), cfg, ghc)
	if err != nil {
		t.Fatalf("resolvePatternComponents error: %v", err)
	}
	if called {
		t.Fatalf("ListBranches should not be called when branch_patterns is empty")
	}
	if len(resolved.Components) != 1 || resolved.Components[0].Name != "tektoncd-pipeline" {
		t.Fatalf("unexpected resolved components: %+v", resolved.Components)
	}
}

func TestResolvePatternComponentsExpandsRegexBranches(t *testing.T) {
	calls := 0
	ghc := gh.NewClient("TestGroup", fakeRunner{fn: func(args ...string) ([]byte, []byte, error) {
		joined := strings.Join(args, " ")
		if joined != "api --paginate repos/TestGroup/tektoncd-pipeline/branches?per_page=100 --jq .[].name" {
			return nil, []byte("unexpected args"), errors.New("unexpected")
		}
		calls++
		return []byte("release-1.10\nfeature-x\nmain\nrelease-1.9\n"), nil, nil
	}})
	cfg := config.RuntimeConfig{
		Components: []config.LoadedComponent{
			{
				Name:           "tektoncd-pipeline",
				Repo:           "tektoncd-pipeline",
				BranchPatterns: []string{"^main$", "^release-[0-9]+\\.[0-9]+$"},
				Pipelines:      []config.PipelineSpec{{Name: "tp-all-in-one"}},
			},
			{
				Name:      "tektoncd-hub",
				Repo:      "tektoncd-hub",
				Branch:    "main",
				Pipelines: []config.PipelineSpec{{Name: "th-all-in-one"}},
			},
		},
	}

	resolved, err := resolvePatternComponents(context.Background(), cfg, ghc)
	if err != nil {
		t.Fatalf("resolvePatternComponents error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("ListBranches call count=%d, want 1", calls)
	}
	if got := len(resolved.Components); got != 4 {
		t.Fatalf("resolved components len=%d, want 4", got)
	}
	gotNames := map[string]struct{}{}
	for _, c := range resolved.Components {
		gotNames[c.Name] = struct{}{}
	}
	for _, want := range []string{
		"tektoncd-pipeline@main",
		"tektoncd-pipeline@release-1.10",
		"tektoncd-pipeline@release-1.9",
		"tektoncd-hub",
	} {
		if _, ok := gotNames[want]; !ok {
			t.Fatalf("missing expanded component %q, got=%v", want, gotNames)
		}
	}
}
