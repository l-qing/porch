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
	prs           string
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
	cmd.Flags().StringVar(&opts.prs, "prs", "", "comma-separated pull request numbers, e.g. 123,456")
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
	prNumbers, err := parsePRNumbers(opts.prs)
	if err != nil {
		return err
	}
	if len(prNumbers) > 0 && strings.TrimSpace(opts.branch) != "" {
		return fmt.Errorf("--prs and --branch are mutually exclusive")
	}
	if len(prNumbers) == 0 {
		cfg, err = resolvePatternComponents(ctx, cfg, ghc)
		if err != nil {
			return err
		}
	}

	componentName := strings.TrimSpace(opts.componentName)
	pipelineName := strings.TrimSpace(opts.pipelineName)
	target := &config.LoadedComponent{}
	if len(prNumbers) > 0 {
		target, err = resolveRetryTargetForPR(cfg.Components, componentName, pipelineName)
	} else {
		target, err = resolveRetryTarget(cfg.Components, componentName, strings.TrimSpace(opts.branch), pipelineName)
	}
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
		"prs":       normalize(strings.TrimSpace(opts.prs)),
		"force":     fmt.Sprintf("%t", opts.force),
		"dry_run":   fmt.Sprintf("%t", opts.dryRun),
		"config":    opts.configPath,
	}).Info("retry command initialized")

	type retryRef struct {
		branch string
		sha    string
		pr     int
	}
	refs := make([]retryRef, 0, 1)
	if len(prNumbers) == 0 {
		refs = append(refs, retryRef{branch: branch})
	} else {
		for _, pr := range prNumbers {
			info, err := ghc.PullRequest(ctx, target.Repo, pr)
			if err != nil {
				return err
			}
			if strings.TrimSpace(info.State) != "open" {
				return fmt.Errorf("pull request %s/%s#%d is not open (state=%s)", cfg.Root.Connection.GitHubOrg, target.Repo, pr, info.State)
			}
			headSHA := strings.TrimSpace(info.Head.SHA)
			if headSHA == "" {
				return fmt.Errorf("pull request %s/%s#%d has empty head sha", cfg.Root.Connection.GitHubOrg, target.Repo, pr)
			}
			refs = append(refs, retryRef{
				branch: strings.TrimSpace(info.Head.Ref),
				sha:    headSHA,
				pr:     info.Number,
			})
		}
	}

	matchedTotal := 0
	triggered := 0
	skippedSucceeded := 0
	for _, ref := range refs {
		sha := strings.TrimSpace(ref.sha)
		if sha == "" {
			refSHA, err := ghc.BranchSHA(ctx, target.Repo, ref.branch)
			if err != nil {
				return err
			}
			sha = refSHA
		}
		checkRuns, err := ghc.CheckRuns(ctx, target.Repo, sha)
		if err != nil {
			// Manual retry should remain available even when status lookup is degraded.
			logEvent(log, "RETRY_WARN", fmt.Sprintf("check-runs query failed, fallback to direct retry trigger: %v", err), logrus.Fields{
				"component": target.Name,
				"branch":    ref.branch,
				"pr":        fmt.Sprintf("%d", ref.pr),
				"sha":       sha[:8],
			})
			checkRuns = nil
		}

		for _, p := range target.Pipelines {
			if pipelineName != "" && p.Name != pipelineName {
				continue
			}
			matchedTotal++

			if !opts.force && shouldSkipRetryBySuccess(checkRuns, p.Name) {
				skippedSucceeded++
				// Default behavior is conservative: do not retrigger already-successful
				// pipeline unless user explicitly opts in with --force.
				logEvent(log, "MANUAL_SKIP", "pipeline already succeeded on current commit, skip retry", logrus.Fields{
					"component": target.Name,
					"pipeline":  p.Name,
					"branch":    ref.branch,
					"pr":        fmt.Sprintf("%d", ref.pr),
					"sha":       sha[:8],
				})
				continue
			}

			body := strings.ReplaceAll(p.RetryCommand, "{branch}", ref.branch)
			logEvent(log, "MANUAL_RETRY", "trigger retry comment", logrus.Fields{
				"component": target.Name,
				"pipeline":  p.Name,
				"branch":    ref.branch,
				"pr":        fmt.Sprintf("%d", ref.pr),
				"sha":       sha[:8],
				"command":   body,
				"dry_run":   fmt.Sprintf("%t", opts.dryRun),
			})

			if opts.dryRun {
				continue
			}
			if ref.pr > 0 {
				if err := ghc.CreatePullRequestComment(ctx, target.Repo, ref.pr, body); err != nil {
					return err
				}
			} else {
				if err := ghc.CreateCommitComment(ctx, target.Repo, sha, body); err != nil {
					return err
				}
			}
			triggered++
		}
	}

	if pipelineName != "" && matchedTotal == 0 && !opts.dryRun {
		return fmt.Errorf("pipeline %q not found under component %q", pipelineName, target.Name)
	}

	if opts.dryRun {
		fmt.Printf("dry-run finished, to-trigger=%d, skipped_succeeded=%d\n", matchedTotal-skippedSucceeded, skippedSucceeded)
	} else {
		fmt.Printf("triggered %d retry command(s), skipped %d succeeded pipeline(s)\n", triggered, skippedSucceeded)
	}
	return nil
}

func resolveRetryTargetForPR(components []config.LoadedComponent, componentName, pipelineName string) (*config.LoadedComponent, error) {
	selected := matchComponentsBySelector(components, componentName)
	if len(selected) == 0 {
		if pipelineName == "" {
			return nil, fmt.Errorf("component %q not found", componentName)
		}
		copy := buildAdHocComponent(componentName, pipelineName, "")
		return &copy, nil
	}
	repo := strings.TrimSpace(selected[0].Repo)
	if repo == "" {
		return nil, fmt.Errorf("component %q repo is empty", componentName)
	}
	for _, c := range selected[1:] {
		if strings.TrimSpace(c.Repo) != repo {
			return nil, fmt.Errorf("component %q maps to multiple repos in config", componentName)
		}
	}
	base := selected[0]
	if pipelineName != "" {
		filtered := make([]config.PipelineSpec, 0, 1)
		for _, p := range base.Pipelines {
			if p.Name == pipelineName {
				filtered = append(filtered, p)
			}
		}
		if len(filtered) == 0 {
			return nil, fmt.Errorf("pipeline %q not found under component %q", pipelineName, componentName)
		}
		base.Pipelines = filtered
	}
	base.Name = componentName
	base.Branch = ""
	return &base, nil
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
