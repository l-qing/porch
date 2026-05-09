package config

import "time"

type Root struct {
	APIVersion         string             `yaml:"apiVersion"`
	Kind               string             `yaml:"kind"`
	Metadata           Metadata           `yaml:"metadata"`
	Connection         Connection         `yaml:"connection"`
	Watch              Watch              `yaml:"watch"`
	Retry              Retry              `yaml:"retry"`
	Timeout            Timeout            `yaml:"timeout"`
	Notification       Notification       `yaml:"notification"`
	Log                Log                `yaml:"log"`
	DisableFinalAction bool               `yaml:"disable_final_action"`
	ComponentsFile     string             `yaml:"components_file"`
	Components         []ComponentSpec    `yaml:"components"`
	Dependencies       map[string]Depends `yaml:"dependencies"`
	FinalAction        FinalAction        `yaml:"final_action"`
}

type Metadata struct {
	Name string `yaml:"name"`
}

type Connection struct {
	Kubeconfig             string `yaml:"kubeconfig"`
	Context                string `yaml:"context"`
	GitHubOrg              string `yaml:"github_org"`
	PipelineConsoleBaseURL string `yaml:"pipeline_console_base_url"`
	PipelineWorkspaceName  string `yaml:"pipeline_workspace_name"`
}

type Watch struct {
	Interval         Duration `yaml:"interval"`
	ExitAfterFinalOK bool     `yaml:"exit_after_final_ok"`
}

type Retry struct {
	MaxRetries       int      `yaml:"max_retries"`
	Backoff          Backoff  `yaml:"backoff"`
	RetrySettleDelay Duration `yaml:"retry_settle_delay"`
}

type Backoff struct {
	Initial    Duration `yaml:"initial"`
	Multiplier float64  `yaml:"multiplier"`
	Max        Duration `yaml:"max"`
}

type Timeout struct {
	Global Duration `yaml:"global"`
}

type Notification struct {
	WecomWebhook         string   `yaml:"wecom_webhook"`
	Events               []string `yaml:"events"`
	ProgressInterval     Duration `yaml:"progress_interval"`
	NotifyRowsPerMessage int      `yaml:"notify_rows_per_message"`
	// NotifyComponentSuccess controls per-component first-success Wecom notifications.
	// nil means use the default (true). Set to false to opt out and fall back to the
	// pre-2026-05 behavior where only the global all_succeeded summary fires.
	NotifyComponentSuccess *bool `yaml:"notify_component_success"`
	// SuppressSucceededInProgress hides components that have already received a
	// per-component success notification from subsequent progress reports and the
	// terminal table. nil means use the default (true). Set to false to keep the
	// pre-2026-05 behavior of always re-listing succeeded rows.
	SuppressSucceededInProgress *bool `yaml:"suppress_succeeded_in_progress"`
}

// NotifyComponentSuccessEnabled returns true when per-component success
// notifications should fire. nil pointer (field omitted in YAML) defaults to true.
func (n Notification) NotifyComponentSuccessEnabled() bool {
	if n.NotifyComponentSuccess == nil {
		return true
	}
	return *n.NotifyComponentSuccess
}

// SuppressSucceededInProgressEnabled returns true when components that already
// received a first-success notification should be hidden from subsequent
// progress reports and from the terminal renderer. nil defaults to true.
func (n Notification) SuppressSucceededInProgressEnabled() bool {
	if n.SuppressSucceededInProgress == nil {
		return true
	}
	return *n.SuppressSucceededInProgress
}

type Log struct {
	File  string `yaml:"file"`
	Level string `yaml:"level"`
}

type ComponentSpec struct {
	Name      string         `yaml:"name"`
	Repo      string         `yaml:"repo"`
	Branches  []string       `yaml:"branches"`
	Patterns  []string       `yaml:"branch_patterns"`
	Pipelines []PipelineSpec `yaml:"pipelines"`
}

type PipelineSpec struct {
	Name         string `yaml:"name"`
	RetryCommand string `yaml:"retry_command"`
}

type Depends struct {
	DependsOn []string `yaml:"depends_on"`
}

type FinalAction struct {
	Repo                string `yaml:"repo"`
	Branch              string `yaml:"branch"`
	BranchFromComponent string `yaml:"branch_from_component"`
	Comment             string `yaml:"comment"`
}

type ComponentsFile map[string]ComponentRevision

type ComponentRevision struct {
	Revision  string   `yaml:"revision"`
	Revisions []string `yaml:"revisions"`
}

type LoadedComponent struct {
	Name           string
	Repo           string
	Branch         string
	BranchPatterns []string
	Pipelines      []PipelineSpec
	PRNumber       int
}

type RuntimeConfig struct {
	Root       Root
	Components []LoadedComponent
}

type Duration struct {
	time.Duration
}
