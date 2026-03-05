package resolver

import (
	"fmt"

	"porch/pkg/config"
)

type DAG struct {
	deps map[string][]string
}

func New(components []config.LoadedComponent, dependencies map[string]config.Depends) (*DAG, error) {
	nodes := map[string]struct{}{}
	for _, c := range components {
		nodes[c.Name] = struct{}{}
	}

	// Build a fully materialized dependency map for all runtime components.
	// Missing dependency spec means "no dependency", not "unknown component".
	deps := map[string][]string{}
	for _, c := range components {
		deps[c.Name] = nil
	}
	for name, d := range dependencies {
		if _, ok := nodes[name]; !ok {
			return nil, fmt.Errorf("dependency target %q is not a known component", name)
		}
		for _, dep := range d.DependsOn {
			if _, ok := nodes[dep]; !ok {
				return nil, fmt.Errorf("component %q depends on unknown component %q", name, dep)
			}
			deps[name] = append(deps[name], dep)
		}
	}

	if err := detectCycle(deps); err != nil {
		return nil, err
	}

	return &DAG{deps: deps}, nil
}

func (d *DAG) IsReady(component string, succeeded map[string]bool) bool {
	deps := d.deps[component]
	for _, dep := range deps {
		if !succeeded[dep] {
			return false
		}
	}
	return true
}

func detectCycle(deps map[string][]string) error {
	const (
		visiting = 1
		done     = 2
	)
	state := map[string]int{}
	var dfs func(string) error
	dfs = func(n string) error {
		// Three-color DFS:
		// visiting -> currently on stack, done -> fully explored.
		if state[n] == visiting {
			return fmt.Errorf("dependency cycle detected at %q", n)
		}
		if state[n] == done {
			return nil
		}
		state[n] = visiting
		for _, dep := range deps[n] {
			if err := dfs(dep); err != nil {
				return err
			}
		}
		state[n] = done
		return nil
	}

	for n := range deps {
		if err := dfs(n); err != nil {
			return err
		}
	}
	return nil
}
