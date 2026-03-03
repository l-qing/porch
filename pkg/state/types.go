package state

import "time"

type File struct {
	Version     int                  `json:"version"`
	StartedAt   time.Time            `json:"started_at"`
	UpdatedAt   time.Time            `json:"updated_at"`
	Components  map[string]Component `json:"components"`
	FinalAction FinalActionState     `json:"final_action"`
}

type FinalActionState struct {
	Triggered   bool       `json:"triggered"`
	TriggeredAt *time.Time `json:"triggered_at"`
}

type Component struct {
	Branch    string                   `json:"branch"`
	SHA       string                   `json:"sha"`
	Namespace string                   `json:"namespace"`
	Pipelines map[string]PipelineState `json:"pipelines"`
}

type PipelineState struct {
	Status      string     `json:"status"`
	PipelineRun string     `json:"pipelinerun_name"`
	RetryCount  int        `json:"retry_count"`
	LastRetryAt *time.Time `json:"last_retry_at"`
	CompletedAt *time.Time `json:"completed_at"`
	RetryAfter  *time.Time `json:"retry_after,omitempty"`
	SettleAfter *time.Time `json:"settle_after,omitempty"`
}
