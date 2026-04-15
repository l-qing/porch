package main

import (
	"strings"

	"porch/pkg/config"
)

func matchComponentsBySelector(components []config.LoadedComponent, selector string) []config.LoadedComponent {
	exact := make([]config.LoadedComponent, 0, 1)
	byBase := make([]config.LoadedComponent, 0, 4)
	byRepo := make([]config.LoadedComponent, 0, 4)
	for i := range components {
		c := components[i]
		if c.Name == selector {
			exact = append(exact, c)
		}
		if runtimeComponentBaseName(c) == selector {
			byBase = append(byBase, c)
		}
		if c.Repo == selector {
			byRepo = append(byRepo, c)
		}
	}
	switch {
	case len(exact) > 0:
		return exact
	case len(byBase) > 0:
		return byBase
	default:
		return byRepo
	}
}

// normalizePipelineSpecForScope normalizes a pipeline spec and, when extraArgs
// is non-empty, overrides the RetryCommand so CLI-provided PAC arguments take
// precedence over any retry_command pre-declared in config. The normalized
// Name always stays bare so check-run matching continues to work.
func normalizePipelineSpecForScope(spec config.PipelineSpec, extraArgs string) config.PipelineSpec {
	normalized := config.NormalizePipelineSpec(spec)
	if strings.TrimSpace(extraArgs) != "" {
		normalized.RetryCommand = config.DefaultRetryCommandWithArgs(normalized.Name, extraArgs)
	}
	return normalized
}

// buildAdHocComponent constructs a transient component when --pipeline is used
// for a pipeline not pre-declared in config. extraArgs carries PAC-style
// arguments parsed from the --pipeline flag (e.g. "version_scope=all") so they
// are appended to the generated /test comment while the pipeline Name stays
// bare for check-run matching.
func buildAdHocComponent(repo, pipeline, extraArgs, branch string) config.LoadedComponent {
	effectiveBranch := strings.TrimSpace(branch)
	if effectiveBranch == "" {
		effectiveBranch = "main"
	}
	return config.LoadedComponent{
		Name:   repo,
		Repo:   repo,
		Branch: effectiveBranch,
		Pipelines: []config.PipelineSpec{{
			Name:         pipeline,
			RetryCommand: config.DefaultRetryCommandWithArgs(pipeline, extraArgs),
		}},
	}
}
