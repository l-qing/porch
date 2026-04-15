package config

import (
	"fmt"
	"strings"
	"unicode"
)

// DefaultRetryCommand builds the standard retry comment body for a pipeline.
func DefaultRetryCommand(pipelineName string) string {
	return DefaultRetryCommandWithArgs(pipelineName, "")
}

// DefaultRetryCommandWithArgs builds the retry comment body and optionally
// appends PAC-style extra arguments (e.g. "version_scope=all image_build_enabled=false")
// between the pipeline name and the branch selector.
// The pipeline name alone is always used for check-run matching; extra args
// only affect the generated /test comment.
func DefaultRetryCommandWithArgs(pipelineName, extraArgs string) string {
	name := strings.TrimSpace(pipelineName)
	if name == "" {
		return ""
	}
	args := strings.TrimSpace(extraArgs)
	if args == "" {
		return fmt.Sprintf("/test %s branch:{branch}", name)
	}
	return fmt.Sprintf("/test %s %s branch:{branch}", name, args)
}

// SplitPipelineArg splits a CLI --pipeline value into the bare pipeline name
// (first whitespace-separated token, used for check-run matching) and the
// remainder treated as PAC-style extra arguments appended to the /test comment.
// Input "catalog-all-e2e-test version_scope=all" yields
// ("catalog-all-e2e-test", "version_scope=all").
func SplitPipelineArg(input string) (name, extraArgs string) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "", ""
	}
	// Split on the first run of whitespace so tabs or multiple spaces are handled.
	idx := strings.IndexFunc(trimmed, unicode.IsSpace)
	if idx < 0 {
		return trimmed, ""
	}
	name = trimmed[:idx]
	extraArgs = strings.TrimSpace(trimmed[idx:])
	return name, extraArgs
}

// NormalizePipelineSpec trims pipeline fields and applies default retry command
// when retry_command is omitted from configuration.
func NormalizePipelineSpec(spec PipelineSpec) PipelineSpec {
	normalized := PipelineSpec{
		Name:         strings.TrimSpace(spec.Name),
		RetryCommand: strings.TrimSpace(spec.RetryCommand),
	}
	if normalized.RetryCommand == "" {
		normalized.RetryCommand = DefaultRetryCommand(normalized.Name)
	}
	return normalized
}

// NormalizePipelineSpecs applies NormalizePipelineSpec to all entries.
func NormalizePipelineSpecs(specs []PipelineSpec) []PipelineSpec {
	normalized := make([]PipelineSpec, 0, len(specs))
	for _, spec := range specs {
		normalized = append(normalized, NormalizePipelineSpec(spec))
	}
	return normalized
}
