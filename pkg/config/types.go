package config

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// DefaultNotificationTimezone is the fallback IANA timezone used to render
// user-facing timestamps when neither the TZ environment variable nor the
// notification.timezone YAML field is set. Beijing time matches the default
// audience for porch's WeCom notifications.
const DefaultNotificationTimezone = "Asia/Shanghai"

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
	// Timezone is the IANA timezone (e.g. "Asia/Shanghai", "UTC",
	// "America/Los_Angeles") used to render user-facing timestamps in WeCom
	// notifications, terminal output and logs. Resolution priority:
	//   1. TZ environment variable (if set and parseable)
	//   2. This YAML field (if set and parseable)
	//   3. DefaultNotificationTimezone (Asia/Shanghai)
	// An invalid value falls through to the next source rather than failing
	// startup; the parse error is returned by ResolveLocation for the caller
	// to surface as a warning.
	Timezone string `yaml:"timezone"`
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

// ResolveLocation picks the time.Location used to render user-facing
// timestamps. Priority: TZ env var > Notification.Timezone YAML field >
// DefaultNotificationTimezone. The returned label is the IANA name used to
// load the location (so callers can render an unambiguous "(Asia/Shanghai)"
// suffix without relying on the location's String() value, which can collapse
// to "Local" once time.Local has been reassigned).
//
// If a higher-priority source is set but fails to parse, the error is
// captured and the next source is tried. The first such error is returned so
// the caller can log a warning. The default fallback (Asia/Shanghai) is part
// of the standard tzdata bundled with the Go runtime; if that also fails to
// load (e.g. a stripped container without zoneinfo), time.UTC is returned and
// the load error is surfaced.
func (n Notification) ResolveLocation() (*time.Location, string, error) {
	var firstErr error
	candidates := []struct {
		source string
		name   string
	}{
		{"TZ env", strings.TrimSpace(os.Getenv("TZ"))},
		{"notification.timezone", strings.TrimSpace(n.Timezone)},
		{"default", DefaultNotificationTimezone},
	}
	for _, c := range candidates {
		if c.name == "" {
			continue
		}
		loc, err := time.LoadLocation(c.name)
		if err == nil {
			return loc, c.name, firstErr
		}
		if firstErr == nil {
			firstErr = fmt.Errorf("load timezone %q from %s: %w", c.name, c.source, err)
		}
	}
	// Final safety net: tzdata is missing entirely. Fall back to UTC so the
	// process keeps running, but propagate the first error so the caller
	// can warn the user that their requested zone never applied.
	return time.UTC, "UTC", firstErr
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
