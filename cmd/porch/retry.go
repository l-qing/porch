package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"porch/pkg/component"
	"porch/pkg/config"
	"porch/pkg/gh"
	pipestatus "porch/pkg/pipeline"
	"porch/pkg/watcher"
)

type retryOptions struct {
	commonOptions
	componentName string
	pipelineName  string
	branch        string
	force         bool
	dryRun        bool
}

func newRetryCmd() *cobra.Command {
	opts := retryOptions{}
	cmd := &cobra.Command{
		Use:   "retry",
		Short: "Trigger manual retry for component/pipeline",
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.complete(viperKeyRetryConfig)
			if strings.TrimSpace(opts.componentName) == "" {
				return fmt.Errorf("--component is required")
			}
			return runRetry(opts)
		},
	}
	cmd.Flags().StringVarP(&opts.configPath, "config", "c", "", "config file path")
	cmd.Flags().StringVar(&opts.componentName, "component", "", "component name")
	cmd.Flags().StringVar(&opts.pipelineName, "pipeline", "", "pipeline name")
	cmd.Flags().StringVar(&opts.branch, "branch", "", "override target branch at runtime")
	cmd.Flags().BoolVar(&opts.force, "force", false, "force retry even if pipeline already succeeded")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "do not send gh comments")
	_ = cmd.MarkFlagRequired("component")
	mustBindPFlag(viperKeyRetryConfig, cmd, "config")
	return cmd
}

func runRetry(opts retryOptions) error {
	cfg, err := loadRuntimeConfig(opts.commonOptions)
	if err != nil {
		return err
	}
	log, closeLog, err := initLogger(cfg, opts.commonOptions)
	if err != nil {
		return err
	}
	defer func() { _ = closeLog() }()

	ghc := gh.NewClient(cfg.Root.Connection.GitHubOrg, nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cfg, err = resolvePatternComponents(ctx, cfg, ghc)
	if err != nil {
		return err
	}

	target, err := resolveRetryTarget(cfg.Components, strings.TrimSpace(opts.componentName), strings.TrimSpace(opts.branch), strings.TrimSpace(opts.pipelineName))
	if err != nil {
		return err
	}
	branch := target.Branch
	if strings.TrimSpace(opts.branch) != "" {
		branch = strings.TrimSpace(opts.branch)
	}
	log.WithFields(logrus.Fields{
		"component": opts.componentName,
		"pipeline":  normalize(opts.pipelineName),
		"branch":    branch,
		"force":     fmt.Sprintf("%t", opts.force),
		"dry_run":   fmt.Sprintf("%t", opts.dryRun),
		"config":    opts.configPath,
	}).Info("retry command initialized")

	sha, err := ghc.BranchSHA(ctx, target.Repo, branch)
	if err != nil {
		return err
	}

	checkRuns, err := ghc.CheckRuns(ctx, target.Repo, sha)
	if err != nil {
		// Manual retry should remain available even when status lookup is degraded.
		logEvent(log, "RETRY_WARN", fmt.Sprintf("check-runs query failed, fallback to direct retry trigger: %v", err), logrus.Fields{
			"component": target.Name,
			"branch":    branch,
			"sha":       sha[:8],
		})
		checkRuns = nil
	}

	matched := 0
	triggered := 0
	skippedSucceeded := 0
	for _, p := range target.Pipelines {
		if opts.pipelineName != "" && p.Name != opts.pipelineName {
			continue
		}
		matched++

		if !opts.force && shouldSkipRetryBySuccess(checkRuns, p.Name) {
			skippedSucceeded++
			// Default behavior is conservative: do not retrigger already-successful
			// pipeline unless user explicitly opts in with --force.
			logEvent(log, "MANUAL_SKIP", "pipeline already succeeded on current commit, skip retry", logrus.Fields{
				"component": target.Name,
				"pipeline":  p.Name,
				"branch":    branch,
				"sha":       sha[:8],
			})
			continue
		}

		body := strings.ReplaceAll(p.RetryCommand, "{branch}", branch)
		logEvent(log, "MANUAL_RETRY", "trigger retry comment", logrus.Fields{
			"component": target.Name,
			"pipeline":  p.Name,
			"branch":    branch,
			"sha":       sha[:8],
			"command":   body,
			"dry_run":   fmt.Sprintf("%t", opts.dryRun),
		})

		if opts.dryRun {
			continue
		}
		if err := ghc.CreateCommitComment(ctx, target.Repo, sha, body); err != nil {
			return err
		}
		triggered++
	}

	if opts.pipelineName != "" && matched == 0 && !opts.dryRun {
		return fmt.Errorf("pipeline %q not found under component %q", opts.pipelineName, target.Name)
	}

	if opts.dryRun {
		fmt.Printf("dry-run finished, to-trigger=%d, skipped_succeeded=%d\n", matched-skippedSucceeded, skippedSucceeded)
	} else {
		fmt.Printf("triggered %d retry command(s), skipped %d succeeded pipeline(s)\n", triggered, skippedSucceeded)
	}
	return nil
}

func resolveRetryTarget(components []config.LoadedComponent, componentName, branch, pipelineName string) (*config.LoadedComponent, error) {
	selected := matchComponentsBySelector(components, componentName)
	if len(selected) == 0 {
		if pipelineName != "" {
			copy := buildAdHocComponent(componentName, pipelineName, branch)
			return &copy, nil
		}
		return nil, fmt.Errorf("component %q not found", componentName)
	}

	if branch != "" {
		for i := range selected {
			if selected[i].Branch == branch {
				copy := selected[i]
				return &copy, nil
			}
		}
		if len(selected) == 1 {
			copy := selected[0]
			copy.Branch = branch
			return &copy, nil
		}
		return nil, fmt.Errorf("branch %q not found under component %q", branch, componentName)
	}

	if len(selected) > 1 {
		choices := make([]string, 0, len(selected))
		for _, c := range selected {
			choices = append(choices, c.Name)
		}
		return nil, fmt.Errorf("component %q matches multiple branches %v, please specify --branch or use component@branch", componentName, choices)
	}
	copy := selected[0]
	return &copy, nil
}

func shouldSkipRetryBySuccess(checkRuns []gh.CheckRun, pipeline string) bool {
	if len(checkRuns) == 0 {
		return false
	}
	run, ok := component.FindPipelineCheckRun(checkRuns, pipeline)
	if !ok {
		return false
	}
	return watcher.ProbeFromCheckRun(run.Status, run.Conclusion).Status == pipestatus.StatusSucceeded
}
