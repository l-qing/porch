package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"porch/pkg/component"
	"porch/pkg/gh"
	pipestatus "porch/pkg/pipeline"
	"porch/pkg/tui"
	"porch/pkg/watcher"
)

type statusOptions struct {
	commonOptions
}

func newStatusCmd() *cobra.Command {
	opts := statusOptions{}
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Query current pipeline status once",
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.complete(viperKeyStatusConfig)
			return runStatus(opts)
		},
	}
	cmd.Flags().StringVarP(&opts.configPath, "config", "c", "", "config file path")
	mustBindPFlag(viperKeyStatusConfig, cmd, "config")
	return cmd
}

func runStatus(opts statusOptions) error {
	cfg, err := loadRuntimeConfig(opts.commonOptions)
	if err != nil {
		return err
	}
	mode, err := resolveProbeMode(opts.probeMode, cfg)
	if err != nil {
		return err
	}

	log, closeLog, err := initLogger(cfg, opts.commonOptions)
	if err != nil {
		return err
	}
	defer func() { _ = closeLog() }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	ghc := gh.NewClient(cfg.Root.Connection.GitHubOrg, nil)
	cfg, err = resolvePatternComponents(ctx, cfg, ghc)
	if err != nil {
		return err
	}
	components, err := component.Initialize(ctx, cfg, ghc)
	if err != nil {
		return err
	}

	log.WithFields(logrus.Fields{
		"components":      fmt.Sprintf("%d", len(components)),
		"config":          opts.configPath,
		"components_file": cfg.Root.ComponentsFile,
		"probe_mode":      string(mode),
	}).Info("status command initialized")
	logEvent(log, "INIT", fmt.Sprintf("loaded %d components", len(components)), nil)
	logEvent(log, "PROBE_MODE", fmt.Sprintf("probe mode=%s", mode), logrus.Fields{"mode": string(mode)})
	printStatusTable(ctx, log, ghc, mode, cfg.Root.Connection.GitHubOrg, cfg.Root.Connection.Kubeconfig, cfg.Root.Connection.Context, components)
	return nil
}

func printStatusTable(ctx context.Context, log *logrus.Logger, ghc *gh.Client, mode probeMode, org, kubeconfig, kubeContext string, components []component.RuntimeComponent) {
	rows := make([]tui.Row, 0, len(components))
	for _, c := range components {
		for _, p := range c.Pipelines {
			fallback := watcher.ProbeFromCheckRun(p.Status, p.Conclusion)
			st := fallback.Status
			if !useKubectlProbe(mode) {
				if ghc != nil {
					if ghProbe, _, ghErr := fallbackProbeStatusFromGH(ctx, ghc, c, p.Name, p.PipelineRun); ghErr == nil {
						st = ghProbe.Status
					} else {
						log.WithFields(logrus.Fields{
							"component": c.Name,
							"branch":    c.Branch,
							"pipeline":  p.Name,
							"run":       p.PipelineRun,
							"sha":       c.SHA,
							"reason":    "gh_only_lookup_failed",
							"error":     ghErr.Error(),
						}).Debug("gh-only probe failed, keep snapshot status")
					}
				}
			} else if p.Namespace != "" && p.PipelineRun != "" {
				probeCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
				pr, err := watcher.ProbePipelineRun(probeCtx, p.Namespace, p.PipelineRun, kubeconfig, kubeContext)
				cancel()
				if err == nil {
					st = pr.Status
				} else {
					source := "gh_snapshot"
					if ghc != nil {
						if ghProbe, ghSource, ghErr := fallbackProbeStatusFromGH(ctx, ghc, c, p.Name, p.PipelineRun); ghErr == nil {
							st = ghProbe.Status
							source = ghSource
						}
					}
					checksURL := commitChecksURL(org, c.Repo, c.SHA)
					log.WithFields(logrus.Fields{
						"component": c.Name,
						"branch":    c.Branch,
						"pipeline":  p.Name,
						"run":       p.PipelineRun,
						"sha":       c.SHA,
						"reason":    source,
						"checks":    checksURL,
						"error":     err.Error(),
					}).Debug(ghFallbackEventMessage(org, c.Repo, c.SHA))
				}
			}

			rows = append(rows, tui.Row{
				Component: c.Name,
				Branch:    c.Branch,
				Pipeline:  p.Name,
				Status:    st,
				Retries:   0,
				Run:       normalize(p.PipelineRun),
			})
		}
	}
	fmt.Print(tui.TerminalTable(rows))
}

func normalize(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func fallbackProbeStatusFromGH(ctx context.Context, ghc *gh.Client, c component.RuntimeComponent, pipeline, currentRun string) (watcher.ProbeResult, string, error) {
	runs, err := ghc.CheckRuns(ctx, c.Repo, c.SHA)
	if err != nil {
		return watcher.ProbeResult{}, "", err
	}
	if strings.TrimSpace(currentRun) != "" {
		if r, ok := component.FindPipelineCheckRunForRun(runs, pipeline, currentRun); ok {
			return watcher.ProbeFromCheckRun(r.Status, r.Conclusion), "gh_current_sha", nil
		}
		if _, ok := component.FindPipelineCheckRun(runs, pipeline); ok {
			return watcher.ProbeResult{Status: pipestatus.StatusRunning, Reason: "gh_fallback_run_mismatch", Conclusion: pipestatus.ConclusionUnknown}, "gh_current_sha_run_mismatch", nil
		}
		return watcher.ProbeResult{}, "", fmt.Errorf("pipeline %q run %q not found in GH check-runs", pipeline, currentRun)
	}
	if r, ok := component.FindPipelineCheckRun(runs, pipeline); ok {
		return watcher.ProbeFromCheckRun(r.Status, r.Conclusion), "gh_current_sha", nil
	}
	return watcher.ProbeResult{}, "", fmt.Errorf("pipeline %q not found in GH check-runs", pipeline)
}
