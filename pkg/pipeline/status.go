package pipeline

type Status string

const (
	StatusUnknown   Status = "unknown"
	StatusMissing   Status = "missing"
	StatusWatching  Status = "watching"
	StatusRunning   Status = "running"
	StatusFailed    Status = "failed"
	StatusSucceeded Status = "succeeded"
	StatusExhausted Status = "exhausted"
	StatusPending   Status = "pending"
	StatusQueryErr  Status = "query_error"
	StatusBackoff   Status = "backoff"
	StatusSettling  Status = "settling"
	StatusBlocked   Status = "blocked"
	StatusTimeout   Status = "timeout"
)

func (s Status) String() string {
	return string(s)
}
