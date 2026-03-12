package config

import (
	"fmt"
	"strings"
)

// DefaultRetryCommand builds the standard retry comment body for a pipeline.
func DefaultRetryCommand(pipelineName string) string {
	name := strings.TrimSpace(pipelineName)
	if name == "" {
		return ""
	}
	return fmt.Sprintf("/test %s branch:{branch}", name)
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
