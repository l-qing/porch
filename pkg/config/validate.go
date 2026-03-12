package config

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

func ValidateRoot(root Root) error {
	if root.APIVersion == "" {
		return fmt.Errorf("apiVersion is required")
	}
	if root.Kind == "" {
		return fmt.Errorf("kind is required")
	}
	if root.Connection.GitHubOrg == "" {
		return fmt.Errorf("connection.github_org is required")
	}
	if baseURL := strings.TrimSpace(root.Connection.PipelineConsoleBaseURL); baseURL != "" {
		parsed, err := url.ParseRequestURI(baseURL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return fmt.Errorf("connection.pipeline_console_base_url must be an absolute URL")
		}
	}
	if workspaceName := strings.TrimSpace(root.Connection.PipelineWorkspaceName); workspaceName != "" {
		if strings.ContainsAny(workspaceName, "/~") {
			return fmt.Errorf("connection.pipeline_workspace_name must not contain '/' or '~'")
		}
	}
	if root.ComponentsFile == "" {
		return fmt.Errorf("components_file is required")
	}
	if len(root.Components) == 0 {
		return fmt.Errorf("components must not be empty")
	}

	seen := map[string]struct{}{}
	for _, c := range root.Components {
		if c.Name == "" {
			return fmt.Errorf("component.name is required")
		}
		if c.Repo == "" {
			return fmt.Errorf("component %q repo is required", c.Name)
		}
		if _, ok := seen[c.Name]; ok {
			return fmt.Errorf("duplicated component name %q", c.Name)
		}
		seen[c.Name] = struct{}{}
		if len(c.Pipelines) == 0 {
			return fmt.Errorf("component %q pipelines must not be empty", c.Name)
		}
		seenBranch := map[string]struct{}{}
		for _, b := range c.Branches {
			branch := strings.TrimSpace(b)
			if branch == "" {
				return fmt.Errorf("component %q has empty branch in branches", c.Name)
			}
			if _, ok := seenBranch[branch]; ok {
				return fmt.Errorf("component %q has duplicated branch %q", c.Name, branch)
			}
			seenBranch[branch] = struct{}{}
		}
		seenPattern := map[string]struct{}{}
		for _, p := range c.Patterns {
			pattern := strings.TrimSpace(p)
			if pattern == "" {
				return fmt.Errorf("component %q has empty pattern in branch_patterns", c.Name)
			}
			if _, ok := seenPattern[pattern]; ok {
				return fmt.Errorf("component %q has duplicated branch pattern %q", c.Name, pattern)
			}
			seenPattern[pattern] = struct{}{}
			if _, err := regexp.Compile(pattern); err != nil {
				return fmt.Errorf("component %q has invalid branch pattern %q: %w", c.Name, pattern, err)
			}
		}
		for _, p := range c.Pipelines {
			if p.Name == "" {
				return fmt.Errorf("component %q has empty pipeline name", c.Name)
			}
		}
	}

	if root.Watch.Interval.Duration <= 0 {
		return fmt.Errorf("watch.interval must be > 0")
	}
	if root.Retry.MaxRetries < 0 {
		return fmt.Errorf("retry.max_retries must be >= 0")
	}
	if root.Retry.Backoff.Initial.Duration <= 0 {
		return fmt.Errorf("retry.backoff.initial must be > 0")
	}
	if root.Retry.Backoff.Multiplier < 1 {
		return fmt.Errorf("retry.backoff.multiplier must be >= 1")
	}
	if root.Retry.Backoff.Max.Duration < root.Retry.Backoff.Initial.Duration {
		return fmt.Errorf("retry.backoff.max must be >= retry.backoff.initial")
	}
	if root.Timeout.Global.Duration <= 0 {
		return fmt.Errorf("timeout.global must be > 0")
	}
	if root.Notification.NotifyRowsPerMessage < 0 {
		return fmt.Errorf("notification.notify_rows_per_message must be >= 0")
	}
	if lvl := strings.TrimSpace(root.Log.Level); lvl != "" {
		switch strings.ToLower(lvl) {
		case "debug", "info", "warn", "warning", "error":
		default:
			return fmt.Errorf("log.level must be one of debug|info|warn|error")
		}
	}

	return nil
}
