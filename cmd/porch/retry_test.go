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

	target, err := resolveRetryTarget(components, "tektoncd-pipeline", "release-1.8", "")
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

	_, err := resolveRetryTarget(components, "tektoncd-pipeline", "", "")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func TestResolveRetryTargetBuildsAdHocComponentWhenMissingInConfig(t *testing.T) {
	components := []config.LoadedComponent{
		{Name: "tektoncd-pipeline", Repo: "tektoncd-pipeline", Branch: "main"},
	}

	target, err := resolveRetryTarget(components, "tektoncd-operator", "main", "to-all-in-one")
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
