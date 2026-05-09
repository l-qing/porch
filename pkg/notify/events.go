package notify

const (
	EventAllSucceeded   = "all_succeeded"
	EventProgressReport = "progress_report"
	// EventComponentSucceeded fires once per component when it first reaches Succeeded.
	// The watch loop also re-arms it if the component flips back to in-flight (new commit)
	// and later succeeds again, so the notification represents the latest run only.
	EventComponentSucceeded = "component_succeeded"
	EventComponentExhausted = "component_exhausted"
	EventGlobalTimeout      = "global_timeout"
)
