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

func buildAdHocComponent(repo, pipeline, branch string) config.LoadedComponent {
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
			RetryCommand: config.DefaultRetryCommand(pipeline),
		}},
	}
}
