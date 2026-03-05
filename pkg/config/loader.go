package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.ScalarNode {
		return fmt.Errorf("duration must be scalar")
	}
	parsed, err := time.ParseDuration(value.Value)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", value.Value, err)
	}
	d.Duration = parsed
	return nil
}

func Load(orchestratorPath string) (RuntimeConfig, error) {
	return LoadWithOptions(orchestratorPath, LoadOptions{})
}

type LoadOptions struct {
	ComponentsFileOverride string
}

func LoadWithOptions(orchestratorPath string, opts LoadOptions) (RuntimeConfig, error) {
	var out RuntimeConfig

	orchestratorBytes, err := os.ReadFile(orchestratorPath)
	if err != nil {
		return out, fmt.Errorf("read orchestrator file: %w", err)
	}

	if err := yaml.Unmarshal(orchestratorBytes, &out.Root); err != nil {
		return out, fmt.Errorf("parse orchestrator yaml: %w", err)
	}

	if err := ValidateRoot(out.Root); err != nil {
		return out, err
	}

	componentsPath := out.Root.ComponentsFile
	// CLI override always wins over orchestrator.yaml.
	// The resolved path is stored back into runtime config for observability/logging.
	if strings.TrimSpace(opts.ComponentsFileOverride) != "" {
		componentsPath = opts.ComponentsFileOverride
		out.Root.ComponentsFile = opts.ComponentsFileOverride
	}
	if !filepath.IsAbs(componentsPath) {
		componentsPath = filepath.Join(filepath.Dir(orchestratorPath), componentsPath)
	}

	componentsBytes, err := os.ReadFile(componentsPath)
	if err != nil {
		return out, fmt.Errorf("read components file: %w", err)
	}

	revisions := ComponentsFile{}
	if err := yaml.Unmarshal(componentsBytes, &revisions); err != nil {
		return out, fmt.Errorf("parse components yaml: %w", err)
	}

	merged := make([]LoadedComponent, 0, len(out.Root.Components))
	seenRuntimeName := map[string]struct{}{}
	for _, c := range out.Root.Components {
		branches := normalizeBranches(c.Branches)
		if len(branches) > 0 {
			// Highest priority: explicit branches in orchestrator.yaml.
			merged, err = appendExpandedRuntimeComponents(merged, seenRuntimeName, c, branches)
			if err != nil {
				return out, err
			}
			continue
		}

		if len(c.Patterns) > 0 {
			// Pattern-based components are expanded at runtime via GitHub branch listing.
			if _, ok := seenRuntimeName[c.Name]; ok {
				return out, fmt.Errorf("duplicated runtime component name %q, check component name/branches", c.Name)
			}
			seenRuntimeName[c.Name] = struct{}{}
			merged = append(merged, LoadedComponent{
				Name:           c.Name,
				Repo:           c.Repo,
				BranchPatterns: c.Patterns,
				Pipelines:      c.Pipelines,
			})
			continue
		}

		branches = componentBranchesFromFile(c.Name, revisions)
		if len(branches) == 0 {
			// Keep watch/status usable even when some components are absent from revisions.
			fmt.Fprintf(os.Stderr, "WARN: component %q missing revision in components file and no component.branches/branch_patterns provided, skipping\n", c.Name)
			continue
		}
		merged, err = appendExpandedRuntimeComponents(merged, seenRuntimeName, c, branches)
		if err != nil {
			return out, err
		}
	}
	out.Components = merged
	return out, nil
}

func appendExpandedRuntimeComponents(merged []LoadedComponent, seenRuntimeName map[string]struct{}, c ComponentSpec, branches []string) ([]LoadedComponent, error) {
	multi := len(branches) > 1
	for _, branch := range branches {
		name := c.Name
		if multi {
			// Runtime names must be unique to keep state keys stable.
			name = fmt.Sprintf("%s@%s", c.Name, branch)
		}
		if _, ok := seenRuntimeName[name]; ok {
			return nil, fmt.Errorf("duplicated runtime component name %q, check component name/branches", name)
		}
		seenRuntimeName[name] = struct{}{}
		merged = append(merged, LoadedComponent{
			Name:      name,
			Repo:      c.Repo,
			Branch:    branch,
			Pipelines: c.Pipelines,
		})
	}
	return merged, nil
}

func normalizeBranches(raw []string) []string {
	out := make([]string, 0, len(raw))
	seen := map[string]struct{}{}
	for _, rawBranch := range raw {
		branch := strings.TrimSpace(rawBranch)
		if branch == "" {
			continue
		}
		if _, ok := seen[branch]; ok {
			continue
		}
		seen[branch] = struct{}{}
		out = append(out, branch)
	}
	return out
}

func componentBranchesFromFile(name string, revisions ComponentsFile) []string {
	rev, ok := revisions[name]
	if !ok {
		return nil
	}

	raw := make([]string, 0, len(rev.Revisions)+1)
	for _, b := range rev.Revisions {
		raw = append(raw, b)
	}
	raw = append(raw, rev.Revision)
	return normalizeBranches(raw)
}
