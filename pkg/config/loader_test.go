package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadSuccess(t *testing.T) {
	dir := t.TempDir()
	orchestrator := filepath.Join(dir, "orchestrator.yaml")
	components := filepath.Join(dir, "components.yaml")

	orchestratorYAML := `apiVersion: porch/v1
kind: ReleaseOrchestration
metadata:
  name: test
connection:
  kubeconfig: ~/.kube/config
  context: ""
  github_org: TestGroup
watch:
  interval: 30s
  exit_after_final_ok: true
retry:
  max_retries: 10
  backoff:
    initial: 1m
    multiplier: 1.5
    max: 5m
  retry_settle_delay: 90s
timeout:
  global: 12h
notification:
  wecom_webhook: ""
  events: [all_succeeded]
log:
  file: ./.porch-events.log
components_file: ./components.yaml
components:
  - name: tektoncd-pipeline
    repo: tektoncd-pipeline
    pipelines:
      - name: tp-all-in-one
        retry_command: "/test tp-all-in-one branch:{branch}"
final_action:
  repo: tektoncd-operator
  branch: main
  branch_from_component: ""
  comment: "/test to-update-components branch:{branch}"
`

	componentsYAML := `tektoncd-pipeline:
  revision: release-1.6
`

	if err := os.WriteFile(orchestrator, []byte(orchestratorYAML), 0o644); err != nil {
		t.Fatalf("write orchestrator: %v", err)
	}
	if err := os.WriteFile(components, []byte(componentsYAML), 0o644); err != nil {
		t.Fatalf("write components: %v", err)
	}

	cfg, err := Load(orchestrator)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	if got := len(cfg.Components); got != 1 {
		t.Fatalf("components len = %d, want 1", got)
	}
	if cfg.Components[0].Branch != "release-1.6" {
		t.Fatalf("branch = %q, want release-1.6", cfg.Components[0].Branch)
	}
	if !cfg.Root.Watch.ExitAfterFinalOK {
		t.Fatalf("watch.exit_after_final_ok = false, want true")
	}
}

func TestLoadMissingRevision(t *testing.T) {
	dir := t.TempDir()
	orchestrator := filepath.Join(dir, "orchestrator.yaml")
	components := filepath.Join(dir, "components.yaml")

	if err := os.WriteFile(orchestrator, []byte(`apiVersion: porch/v1
kind: ReleaseOrchestration
metadata: {name: test}
connection: {github_org: TestGroup}
watch: {interval: 30s}
retry:
  max_retries: 1
  backoff: {initial: 1m, multiplier: 1.5, max: 5m}
timeout: {global: 1h}
components_file: ./components.yaml
components:
  - name: a
    repo: a
    pipelines:
      - name: p
        retry_command: x
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(components, []byte(`b: {revision: main}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(orchestrator)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if len(cfg.Components) != 0 {
		t.Fatalf("expected component to be skipped, got %d components", len(cfg.Components))
	}
}

func TestLoadWithComponentsFileOverride(t *testing.T) {
	dir := t.TempDir()
	orchestrator := filepath.Join(dir, "orchestrator.yaml")
	defaultComponents := filepath.Join(dir, "components.yaml")
	overrideComponents := filepath.Join(dir, "components-override.yaml")

	if err := os.WriteFile(orchestrator, []byte(`apiVersion: porch/v1
kind: ReleaseOrchestration
metadata: {name: test}
connection: {github_org: TestGroup}
watch: {interval: 30s}
retry:
  max_retries: 1
  backoff: {initial: 1m, multiplier: 1.5, max: 5m}
timeout: {global: 1h}
components_file: ./components.yaml
components:
  - name: a
    repo: a
    pipelines:
      - name: p
        retry_command: x
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(defaultComponents, []byte(`a: {revision: main}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(overrideComponents, []byte(`a: {revision: release-1.6}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadWithOptions(orchestrator, LoadOptions{ComponentsFileOverride: overrideComponents})
	if err != nil {
		t.Fatalf("LoadWithOptions error: %v", err)
	}
	if got := cfg.Components[0].Branch; got != "release-1.6" {
		t.Fatalf("branch = %q, want release-1.6", got)
	}
}

func TestLoadWithComponentBranchesFromOrchestrator(t *testing.T) {
	dir := t.TempDir()
	orchestrator := filepath.Join(dir, "orchestrator.yaml")
	components := filepath.Join(dir, "components.yaml")

	if err := os.WriteFile(orchestrator, []byte(`apiVersion: porch/v1
kind: ReleaseOrchestration
metadata: {name: test}
connection: {github_org: TestGroup}
watch: {interval: 30s}
retry:
  max_retries: 1
  backoff: {initial: 1m, multiplier: 1.5, max: 5m}
timeout: {global: 1h}
components_file: ./components.yaml
components:
  - name: tektoncd-pipeline
    repo: tektoncd-pipeline
    branches: [main, release-1.8, release-1.9]
    pipelines:
      - name: tp-all-in-one
        retry_command: x
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(components, []byte(`tektoncd-pipeline: {revision: release-0.1}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(orchestrator)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if got := len(cfg.Components); got != 3 {
		t.Fatalf("components len = %d, want 3", got)
	}

	gotNames := map[string]struct{}{}
	for _, c := range cfg.Components {
		gotNames[c.Name] = struct{}{}
	}
	for _, want := range []string{
		"tektoncd-pipeline@main",
		"tektoncd-pipeline@release-1.8",
		"tektoncd-pipeline@release-1.9",
	} {
		if _, ok := gotNames[want]; !ok {
			t.Fatalf("missing expanded component %q", want)
		}
	}
}

func TestLoadWithComponentFileRevisions(t *testing.T) {
	dir := t.TempDir()
	orchestrator := filepath.Join(dir, "orchestrator.yaml")
	components := filepath.Join(dir, "components.yaml")

	if err := os.WriteFile(orchestrator, []byte(`apiVersion: porch/v1
kind: ReleaseOrchestration
metadata: {name: test}
connection: {github_org: TestGroup}
watch: {interval: 30s}
retry:
  max_retries: 1
  backoff: {initial: 1m, multiplier: 1.5, max: 5m}
timeout: {global: 1h}
components_file: ./components.yaml
components:
  - name: tektoncd-pipeline
    repo: tektoncd-pipeline
    pipelines:
      - name: tp-all-in-one
        retry_command: x
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(components, []byte(`tektoncd-pipeline:
  revisions:
    - main
    - release-1.8
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(orchestrator)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if got := len(cfg.Components); got != 2 {
		t.Fatalf("components len = %d, want 2", got)
	}
	gotNames := map[string]struct{}{}
	for _, c := range cfg.Components {
		gotNames[c.Name] = struct{}{}
	}
	for _, want := range []string{
		"tektoncd-pipeline@main",
		"tektoncd-pipeline@release-1.8",
	} {
		if _, ok := gotNames[want]; !ok {
			t.Fatalf("missing expanded component %q", want)
		}
	}
}

func TestLoadWithBranchPatternsPreferredOverComponentsFile(t *testing.T) {
	dir := t.TempDir()
	orchestrator := filepath.Join(dir, "orchestrator.yaml")
	components := filepath.Join(dir, "components.yaml")

	if err := os.WriteFile(orchestrator, []byte(`apiVersion: porch/v1
kind: ReleaseOrchestration
metadata: {name: test}
connection: {github_org: TestGroup}
watch: {interval: 30s}
retry:
  max_retries: 1
  backoff: {initial: 1m, multiplier: 1.5, max: 5m}
timeout: {global: 1h}
components_file: ./components.yaml
components:
  - name: tektoncd-pipeline
    repo: tektoncd-pipeline
    branch_patterns: ["^main$", "^release-[0-9]+\\.[0-9]+$"]
    pipelines:
      - name: tp-all-in-one
        retry_command: x
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(components, []byte(`tektoncd-pipeline: {revision: release-0.1}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(orchestrator)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if got := len(cfg.Components); got != 1 {
		t.Fatalf("components len = %d, want 1", got)
	}
	if cfg.Components[0].Name != "tektoncd-pipeline" {
		t.Fatalf("name = %q, want tektoncd-pipeline", cfg.Components[0].Name)
	}
	if cfg.Components[0].Branch != "" {
		t.Fatalf("branch = %q, want empty (defer to runtime regex expansion)", cfg.Components[0].Branch)
	}
	if got := len(cfg.Components[0].BranchPatterns); got != 2 {
		t.Fatalf("branch patterns len = %d, want 2", got)
	}
}

func TestLoadWithBranchesPreferredOverPatterns(t *testing.T) {
	dir := t.TempDir()
	orchestrator := filepath.Join(dir, "orchestrator.yaml")
	components := filepath.Join(dir, "components.yaml")

	if err := os.WriteFile(orchestrator, []byte(`apiVersion: porch/v1
kind: ReleaseOrchestration
metadata: {name: test}
connection: {github_org: TestGroup}
watch: {interval: 30s}
retry:
  max_retries: 1
  backoff: {initial: 1m, multiplier: 1.5, max: 5m}
timeout: {global: 1h}
components_file: ./components.yaml
components:
  - name: tektoncd-pipeline
    repo: tektoncd-pipeline
    branches: [main, release-1.9]
    branch_patterns: ["^main$", "^release-[0-9]+\\.[0-9]+$"]
    pipelines:
      - name: tp-all-in-one
        retry_command: x
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(components, []byte(`tektoncd-pipeline: {revision: release-0.1}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(orchestrator)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if got := len(cfg.Components); got != 2 {
		t.Fatalf("components len = %d, want 2", got)
	}
	gotNames := map[string]struct{}{}
	for _, c := range cfg.Components {
		gotNames[c.Name] = struct{}{}
	}
	for _, want := range []string{
		"tektoncd-pipeline@main",
		"tektoncd-pipeline@release-1.9",
	} {
		if _, ok := gotNames[want]; !ok {
			t.Fatalf("missing expanded component %q", want)
		}
	}
}

func TestLoadWithNotifyRowsPerMessage(t *testing.T) {
	dir := t.TempDir()
	orchestrator := filepath.Join(dir, "orchestrator.yaml")
	components := filepath.Join(dir, "components.yaml")

	if err := os.WriteFile(orchestrator, []byte(`apiVersion: porch/v1
kind: ReleaseOrchestration
metadata: {name: test}
connection: {github_org: TestGroup}
watch: {interval: 30s}
retry:
  max_retries: 1
  backoff: {initial: 1m, multiplier: 1.5, max: 5m}
timeout: {global: 1h}
notification:
  notify_rows_per_message: 7
components_file: ./components.yaml
components:
  - name: tektoncd-pipeline
    repo: tektoncd-pipeline
    pipelines:
      - name: tp-all-in-one
        retry_command: x
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(components, []byte(`tektoncd-pipeline: {revision: main}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(orchestrator)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if got := cfg.Root.Notification.NotifyRowsPerMessage; got != 7 {
		t.Fatalf("notify_rows_per_message = %d, want 7", got)
	}
}

func TestLoadWithPipelineConsoleSettings(t *testing.T) {
	dir := t.TempDir()
	orchestrator := filepath.Join(dir, "orchestrator.yaml")
	components := filepath.Join(dir, "components.yaml")

	if err := os.WriteFile(orchestrator, []byte(`apiVersion: porch/v1
kind: ReleaseOrchestration
metadata: {name: test}
connection:
  github_org: TestGroup
  pipeline_console_base_url: https://edge-prod.alauda.cn/console-pipeline-v2
  pipeline_workspace_name: business-release
watch: {interval: 30s}
retry:
  max_retries: 1
  backoff: {initial: 1m, multiplier: 1.5, max: 5m}
timeout: {global: 1h}
components_file: ./components.yaml
components:
  - name: tektoncd-pipeline
    repo: tektoncd-pipeline
    pipelines:
      - name: tp-all-in-one
        retry_command: x
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(components, []byte(`tektoncd-pipeline: {revision: main}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(orchestrator)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if got := cfg.Root.Connection.PipelineConsoleBaseURL; got != "https://edge-prod.alauda.cn/console-pipeline-v2" {
		t.Fatalf("pipeline_console_base_url = %q, want https://edge-prod.alauda.cn/console-pipeline-v2", got)
	}
	if got := cfg.Root.Connection.PipelineWorkspaceName; got != "business-release" {
		t.Fatalf("pipeline_workspace_name = %q, want business-release", got)
	}
}

func TestLoadRejectsInvalidPipelineConsoleBaseURL(t *testing.T) {
	dir := t.TempDir()
	orchestrator := filepath.Join(dir, "orchestrator.yaml")
	components := filepath.Join(dir, "components.yaml")

	if err := os.WriteFile(orchestrator, []byte(`apiVersion: porch/v1
kind: ReleaseOrchestration
metadata: {name: test}
connection:
  github_org: TestGroup
  pipeline_console_base_url: edge-prod.alauda.cn/console-pipeline-v2
watch: {interval: 30s}
retry:
  max_retries: 1
  backoff: {initial: 1m, multiplier: 1.5, max: 5m}
timeout: {global: 1h}
components_file: ./components.yaml
components:
  - name: tektoncd-pipeline
    repo: tektoncd-pipeline
    pipelines:
      - name: tp-all-in-one
        retry_command: x
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(components, []byte(`tektoncd-pipeline: {revision: main}`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(orchestrator)
	if err == nil {
		t.Fatal("expected error for invalid pipeline_console_base_url")
	}
	if got := err.Error(); got == "" || !containsAll(got, "pipeline_console_base_url", "absolute URL") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadRejectsInvalidPipelineWorkspaceName(t *testing.T) {
	dir := t.TempDir()
	orchestrator := filepath.Join(dir, "orchestrator.yaml")
	components := filepath.Join(dir, "components.yaml")

	if err := os.WriteFile(orchestrator, []byte(`apiVersion: porch/v1
kind: ReleaseOrchestration
metadata: {name: test}
connection:
  github_org: TestGroup
  pipeline_workspace_name: business/release
watch: {interval: 30s}
retry:
  max_retries: 1
  backoff: {initial: 1m, multiplier: 1.5, max: 5m}
timeout: {global: 1h}
components_file: ./components.yaml
components:
  - name: tektoncd-pipeline
    repo: tektoncd-pipeline
    pipelines:
      - name: tp-all-in-one
        retry_command: x
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(components, []byte(`tektoncd-pipeline: {revision: main}`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(orchestrator)
	if err == nil {
		t.Fatal("expected error for invalid pipeline_workspace_name")
	}
	if got := err.Error(); got == "" || !containsAll(got, "pipeline_workspace_name", "must not contain") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func containsAll(text string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(text, part) {
			return false
		}
	}
	return true
}
