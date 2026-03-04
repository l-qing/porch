package pipeline

type Conclusion string

const (
	ConclusionUnknown Conclusion = "unknown"
	ConclusionSuccess Conclusion = "success"
	ConclusionFailure Conclusion = "failure"
)

func (c Conclusion) String() string {
	return string(c)
}
