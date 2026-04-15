package main

import (
	"testing"

	"porch/pkg/config"
)

func TestResolveRetryTargetByBaseAndBranch(t *testing.T) {
	components := []config.LoadedComponent{
		{Name: "tektoncd-pipeline@main", Branch: "main"},
		{Name: "tektoncd-pipeline@release-1.8", Branch: "release-1.8"},
	}

	target, err := resolveRetryTarget(components, "tektoncd-pipeline", "release-1.8", "", "", "")
	if err != nil {
		t.Fatalf("resolveRetryTarget error: %v", err)
	}
	if target.Name != "tektoncd-pipeline@release-1.8" {
		t.Fatalf("target name=%q, want tektoncd-pipeline@release-1.8", target.Name)
	}
}

func TestResolveRetryTargetRequireBranchWhenMultipleMatched(t *testing.T) {
	components := []config.LoadedComponent{
		{Name: "tektoncd-pipeline@main", Branch: "main"},
		{Name: "tektoncd-pipeline@release-1.8", Branch: "release-1.8"},
	}

	_, err := resolveRetryTarget(components, "tektoncd-pipeline", "", "", "", "")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func TestResolveRetryTargetAppliesExtraArgsToDeclaredPipeline(t *testing.T) {
	// Declared pipeline must keep its bare Name so check-run matching works,
	// while extra PAC args from --pipeline override the generated /test command.
	components := []config.LoadedComponent{
		{
			Name:   "catalog",
			Repo:   "catalog",
			Branch: "main",
			Pipelines: []config.PipelineSpec{
				{Name: "catalog-all-e2e-test"},
			},
		},
	}

	target, err := resolveRetryTarget(components, "catalog", "main", "", "catalog-all-e2e-test", "version_scope=all")
	if err != nil {
		t.Fatalf("resolveRetryTarget error: %v", err)
	}
	if len(target.Pipelines) != 1 {
		t.Fatalf("pipelines len=%d, want 1", len(target.Pipelines))
	}
	if got := target.Pipelines[0].Name; got != "catalog-all-e2e-test" {
		t.Fatalf("pipeline name=%q, want catalog-all-e2e-test", got)
	}
	want := "/test catalog-all-e2e-test version_scope=all branch:{branch}"
	if got := target.Pipelines[0].RetryCommand; got != want {
		t.Fatalf("retry command=%q, want %q", got, want)
	}
}

func TestResolveRetryTargetAppliesExtraArgsToAdHocPipeline(t *testing.T) {
	// Ad-hoc pipeline (not declared in config) must also carry extra args
	// through into its synthesized retry command.
	components := []config.LoadedComponent{
		{Name: "catalog", Repo: "catalog", Branch: "main"},
	}

	target, err := resolveRetryTarget(components, "catalog", "main", "", "catalog-all-in-one", "image_build_enabled=false")
	if err != nil {
		t.Fatalf("resolveRetryTarget error: %v", err)
	}
	if len(target.Pipelines) != 1 {
		t.Fatalf("pipelines len=%d, want 1", len(target.Pipelines))
	}
	if got := target.Pipelines[0].Name; got != "catalog-all-in-one" {
		t.Fatalf("pipeline name=%q, want catalog-all-in-one", got)
	}
	want := "/test catalog-all-in-one image_build_enabled=false branch:{branch}"
	if got := target.Pipelines[0].RetryCommand; got != want {
		t.Fatalf("retry command=%q, want %q", got, want)
	}
}

func TestResolveRetryTargetBuildsAdHocComponentWhenMissingInConfig(t *testing.T) {
	components := []config.LoadedComponent{
		{Name: "tektoncd-pipeline", Repo: "tektoncd-pipeline", Branch: "main"},
	}

	target, err := resolveRetryTarget(components, "tektoncd-operator", "main", "", "to-all-in-one", "")
	if err != nil {
		t.Fatalf("resolveRetryTarget error: %v", err)
	}
	if target.Name != "tektoncd-operator" || target.Repo != "tektoncd-operator" || target.Branch != "main" {
		t.Fatalf("unexpected ad-hoc target: %+v", target)
	}
	if len(target.Pipelines) != 1 {
		t.Fatalf("pipelines len=%d, want 1", len(target.Pipelines))
	}
	if target.Pipelines[0].Name != "to-all-in-one" {
		t.Fatalf("pipeline name=%q, want to-all-in-one", target.Pipelines[0].Name)
	}
}
