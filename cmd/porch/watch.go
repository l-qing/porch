package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"porch/pkg/component"
	"porch/pkg/config"
	"porch/pkg/gh"
	"porch/pkg/notify"
	pipestatus "porch/pkg/pipeline"
	"porch/pkg/resolver"
	"porch/pkg/retrier"
	"porch/pkg/state"
	"porch/pkg/tui"
	"porch/pkg/watcher"
)

type trackedPipeline struct {
	Name        string
	RetryCmd    string
	Namespace   string
	PipelineRun string
	Status      pipestatus.Status
	Conclusion  pipestatus.Conclusion
	RetryCount  int
	QueryErrors int
	RunMismatch int
	CompletedAt *time.Time
	RetryAfter  *time.Time
	SettleAfter *time.Time
}

type trackedComponent struct {
	Name      string
	Repo      string
	Branch    string
	SHA       string
	Pipelines map[string]*trackedPipeline
}

type watchOptions struct {
	commonOptions
	stateFile      string
	finalBranch    string
	exitAfterFinal bool
	componentName  string
	pipelineName   string
	branch         string
	branchPattern  string
	dryRun         bool
}

type watchEventKind string

const (
	eventRecoverSkip    watchEventKind = "RECOVER_SKIP"
	eventRecover        watchEventKind = "RECOVER"
	eventRecoverWarn    watchEventKind = "RECOVER_WARN"
	eventInit           watchEventKind = "INIT"
	eventProbeMode      watchEventKind = "PROBE_MODE"
	eventScope          watchEventKind = "SCOPE"
	eventFinalDisabled  watchEventKind = "FINAL_DISABLED"
	eventWatchErr       watchEventKind = "WATCH_ERR"
	eventNotifyErr      watchEventKind = "NOTIFY_ERR"
	eventScopeDone      watchEventKind = "SCOPE_DONE"
	eventAllDone        watchEventKind = "ALL_DONE"
	eventFinalResident  watchEventKind = "FINAL_RESIDENT"
	eventFinalErr       watchEventKind = "FINAL_ERR"
	eventDryFinal       watchEventKind = "DRY_FINAL"
	eventFinalOK        watchEventKind = "FINAL_OK"
	eventStateErr       watchEventKind = "STATE_ERR"
	eventNotifyProgress watchEventKind = "NOTIFY_PROGRESS"
	eventTimeout        watchEventKind = "TIMEOUT"
	eventExit           watchEventKind = "EXIT"
	eventDryRetry       watchEventKind = "DRY_RETRY"
	eventRetryOK        watchEventKind = "RETRY_OK"
	eventGHFallback     watchEventKind = "GH_FALLBACK"
	eventRunMismatch    watchEventKind = "RUN_MISMATCH"
	eventRunStale       watchEventKind = "RUN_STALE"
	eventSuccess        watchEventKind = "SUCCESS"
	eventQueryWarn      watchEventKind = "QUERY_WARN"
	eventQueryErr       watchEventKind = "QUERY_ERR"
	eventExhausted      watchEventKind = "EXHAUSTED"
	eventFailed         watchEventKind = "FAILED"
	eventRetrying       watchEventKind = "RETRYING"
	eventCommitURL      watchEventKind = "COMMIT_URL"
	eventAllOK          watchEventKind = "ALL_OK"
	eventScopeOK        watchEventKind = "SCOPE_OK"
)

const runMismatchRetryThreshold = 3
const defaultQueryErrorThreshold = 5
const defaultNotifyRowsPerMessage = 12
const maxNotifyMarkdownBytes = 3800 // limit 4k
const notifySendTimeout = 10 * time.Second
const probePipelineTimeout = 20 * time.Second
const recoverProbeTimeout = 10 * time.Second
const ghAnnotationTimeout = 8 * time.Second
const chunkEstimatePage = 99

func newWatchCmd() *cobra.Command {
	opts := watchOptions{}
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Continuously watch pipelines and auto-retry",
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.complete(viperKeyWatchConfig)
			opts.stateFile = firstNonEmpty(
				strings.TrimSpace(opts.stateFile),
				strings.TrimSpace(viper.GetString(viperKeyWatchStateFile)),
				defaultStateFile,
			)
			opts.finalBranch = strings.TrimSpace(firstNonEmpty(opts.finalBranch, viper.GetString(viperKeyFinalBranch)))
			if !cmd.Flags().Changed("exit-after-final-ok") {
				opts.exitAfterFinal = viper.GetBool(viperKeyWatchExitAfterDone)
			}
			return runWatch(opts)
		},
	}
	cmd.Flags().StringVarP(&opts.configPath, "config", "c", "", "config file path")
	cmd.Flags().StringVar(&opts.stateFile, "state-file", "", "state file path")
	cmd.Flags().BoolVar(&opts.exitAfterFinal, "exit-after-final-ok", false, "exit immediately after final success")
	cmd.Flags().StringVar(&opts.componentName, "component", "", "watch only one component")
	cmd.Flags().StringVar(&opts.pipelineName, "pipeline", "", "watch only one pipeline under selected component")
	cmd.Flags().StringVar(&opts.branch, "branch", "", "override selected component branch at runtime")
	cmd.Flags().StringVar(&opts.branchPattern, "branch-pattern", "", "select branches by regular expression under selected component")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "query only, do not trigger retry")
	mustBindPFlag(viperKeyWatchConfig, cmd, "config")
	mustBindPFlag(viperKeyWatchStateFile, cmd, "state-file")
	mustBindPFlag(viperKeyWatchExitAfterDone, cmd, "exit-after-final-ok")
	return cmd
}

func runWatch(opts watchOptions) error {
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

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Root.Timeout.Global.Duration)
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	ghc := gh.NewClient(cfg.Root.Connection.GitHubOrg, nil)
	if shouldSkipPatternResolutionForAdHocScope(cfg.Components, opts) {
		log.WithFields(logrus.Fields{
			"component_scope": normalize(strings.TrimSpace(opts.componentName)),
			"pipeline_scope":  normalize(strings.TrimSpace(opts.pipelineName)),
		}).Debug("skip global branch pattern expansion for ad-hoc scoped watch")
	} else {
		cfg, err = resolvePatternComponents(ctx, cfg, ghc)
		if err != nil {
			return err
		}
	}
	notifyRowsPerMessage := resolveNotifyRowsPerMessage(cfg.Root.Notification.NotifyRowsPerMessage)
	scopedMode, err := applyWatchScopeWithBranchLister(ctx, &cfg, opts, ghc)
	if err != nil {
		return err
	}
	log.WithFields(logrus.Fields{
		"config":               opts.configPath,
		"components_file":      cfg.Root.ComponentsFile,
		"state_file":           opts.stateFile,
		"component_scope":      normalize(strings.TrimSpace(opts.componentName)),
		"pipeline_scope":       normalize(strings.TrimSpace(opts.pipelineName)),
		"branch_override":      normalize(strings.TrimSpace(opts.branch)),
		"branch_pattern_scope": normalize(strings.TrimSpace(opts.branchPattern)),
		"scoped_mode":          fmt.Sprintf("%t", scopedMode),
		"final_branch":         normalize(opts.finalBranch),
		"disable_final_action": fmt.Sprintf("%t", opts.disableFinalAction || cfg.Root.DisableFinalAction),
		"exit_after_final":     fmt.Sprintf("%t", opts.exitAfterFinal),
		"watch_interval":       cfg.Root.Watch.Interval.Duration.String(),
		"timeout_global":       cfg.Root.Timeout.Global.Duration.String(),
		"dry_run":              fmt.Sprintf("%t", opts.dryRun),
		"log_level_effect":     log.GetLevel().String(),
		"progress_interval":    cfg.Root.Notification.ProgressInterval.Duration.String(),
		"notify_rows":          fmt.Sprintf("%d", notifyRowsPerMessage),
		"probe_mode":           string(mode),
	}).Info("watch command initialized")
	runtimeComponents, err := component.Initialize(ctx, cfg, ghc)
	if err != nil {
		return err
	}
	startedAt := time.Now().UTC()

	tracked, err := toTracked(cfg, runtimeComponents)
	if err != nil {
		return err
	}

	dependencies := expandRuntimeDependencies(cfg.Components, cfg.Root.Dependencies)
	finalActionEnabled := !(opts.disableFinalAction || cfg.Root.DisableFinalAction)
	if scopedMode {
		dependencies = map[string]config.Depends{}
		finalActionEnabled = false
	}
	dag, err := resolver.New(cfg.Components, dependencies)
	if err != nil {
		return err
	}

	store := state.NewStore(opts.stateFile)
	renderer := tui.NewRenderer()
	wecom := notify.NewWecom(cfg.Root.Notification.WecomWebhook, cfg.Root.Notification.Events)
	exitAfterFinal := opts.exitAfterFinal || cfg.Root.Watch.ExitAfterFinalOK
	finalTriggered := false
	var finalTriggeredAt *time.Time
	allSucceededNotified := false
	finalDisabledResident := false
	emit := func(kind watchEventKind, msg string, fields logrus.Fields) {
		logEvent(log, string(kind), msg, fields)
		renderer.AddEvent(string(kind), msg)

		event := ""
		switch kind {
		case eventExhausted:
			event = notify.EventComponentExhausted
		case eventTimeout:
			event = notify.EventGlobalTimeout
		}
		if event != "" {
			notifyCtx, cancel := context.WithTimeout(context.Background(), notifySendTimeout)
			if err := wecom.Notify(notifyCtx, event, msg); err != nil {
				logEvent(log, string(eventNotifyErr), err.Error(), logrus.Fields{"error": err.Error()})
			}
			cancel()
		}
	}

	if scopedMode {
		emit(eventRecoverSkip, "scoped watch mode: skip state recovery", nil)
	} else if loaded, err := store.Load(); err == nil {
		recoverFromState(ctx, log, mode, tracked, loaded, cfg.Root.Connection.Kubeconfig, cfg.Root.Connection.Context)
		emit(eventRecover, "recovered from existing state file", nil)
	} else if !errors.Is(err, os.ErrNotExist) {
		emit(eventRecoverWarn, fmt.Sprintf("state recovery skipped: %v", err), logrus.Fields{"error": err.Error()})
	}

	if err := store.Save(buildState(startedAt, tracked, finalTriggered, finalTriggeredAt)); err != nil {
		return fmt.Errorf("save initial state: %w", err)
	}
	emit(eventInit, fmt.Sprintf("watching %d components", len(tracked)), logrus.Fields{"components": fmt.Sprintf("%d", len(tracked))})
	emit(eventProbeMode, fmt.Sprintf("probe mode=%s", mode), logrus.Fields{"mode": string(mode)})
	if scopedMode {
		emit(eventScope, "single-component watch mode enabled (DAG ignored, final_action disabled)", nil)
	} else if !finalActionEnabled {
		emit(eventFinalDisabled, "global disable_final_action is enabled, skip final_action trigger", nil)
	}
	emitCommitURLsOnce(cfg, tracked, emit)

	ticker := time.NewTicker(cfg.Root.Watch.Interval.Duration)
	defer ticker.Stop()
	lastProgressAt := time.Now()
	firstCheckDone := false
	processTick := func() (bool, error) {
		if err := watchOnce(ctx, log, cfg, ghc, dag, tracked, mode, opts.dryRun, emit); err != nil {
			emit(eventWatchErr, err.Error(), logrus.Fields{"error": err.Error()})
		}
		if !firstCheckDone {
			firstCheckDone = true
			if !allComponentsSucceeded(tracked) {
				lastProgressAt = time.Time{}
			}
		}

		allSucceeded := allComponentsSucceeded(tracked)
		rows := toRows(tracked, cfg.Root.Connection.GitHubOrg)
		if allSucceeded && !allSucceededNotified {
			now := time.Now().UTC()
			kind := eventAllOK
			msg := "all components succeeded"
			if scopedMode {
				kind = eventScopeOK
				msg = "scoped watch target succeeded"
			}
			emit(kind, msg, nil)
			branch := successSummaryBranch(scopedMode, opts.finalBranch, cfg, tracked, finalActionEnabled)
			if err := notifyMarkdownInChunks(context.Background(), wecom, notify.EventAllSucceeded, rows, notifyRowsPerMessage, func(chunk []tui.Row, page, total int) string {
				return buildFinalOKMarkdown(branch, startedAt, now, chunk, page, total)
			}); err != nil {
				emit(eventNotifyErr, err.Error(), logrus.Fields{"error": err.Error()})
			}
			allSucceededNotified = true
		}

		if scopedMode && exitAfterFinal && allSucceeded {
			renderer.Render(rows)
			emit(eventScopeDone, "scoped watch target succeeded, exiting", nil)
			return true, nil
		}

		if !scopedMode && !finalActionEnabled && allSucceeded {
			if exitAfterFinal {
				renderer.Render(rows)
				emit(eventAllDone, "all components succeeded, final_action disabled, exiting", nil)
				return true, nil
			}
			if !finalDisabledResident {
				emit(eventFinalResident, "all components succeeded, final_action disabled, keep watching for new runs", logrus.Fields{"exit_after_final_ok": "false"})
				finalDisabledResident = true
			}
		}

		if finalActionEnabled && !finalTriggered && allSucceeded {
			branch, err := resolveFinalBranch(opts.finalBranch, cfg, tracked)
			if err != nil {
				emit(eventFinalErr, err.Error(), logrus.Fields{"error": err.Error()})
			} else {
				if opts.dryRun {
					emit(eventDryFinal, fmt.Sprintf("would trigger final_action on %s branch=%s", cfg.Root.FinalAction.Repo, branch), logrus.Fields{"repo": cfg.Root.FinalAction.Repo, "branch": branch})
					finalTriggered = true
					now := time.Now().UTC()
					finalTriggeredAt = &now
					_ = store.Save(buildState(startedAt, tracked, finalTriggered, finalTriggeredAt))
					if exitAfterFinal {
						renderer.Render(rows)
						return true, nil
					}
					emit(eventFinalResident, "final condition reached, keep watching for new runs", logrus.Fields{"exit_after_final_ok": "false"})
				} else {
					if err := triggerFinalAction(ctx, ghc, cfg, branch); err != nil {
						emit(eventFinalErr, err.Error(), logrus.Fields{"error": err.Error(), "repo": cfg.Root.FinalAction.Repo, "branch": branch})
					} else {
						finalTriggered = true
						now := time.Now().UTC()
						finalTriggeredAt = &now
						emit(eventFinalOK, fmt.Sprintf("final_action triggered branch=%s", branch), logrus.Fields{"repo": cfg.Root.FinalAction.Repo, "branch": branch})
						_ = store.Save(buildState(startedAt, tracked, finalTriggered, finalTriggeredAt))
						if exitAfterFinal {
							renderer.Render(rows)
							return true, nil
						}
						emit(eventFinalResident, "final_action triggered, keep watching for new runs", logrus.Fields{"exit_after_final_ok": "false"})
					}
				}
			}
		}

		if err := store.Save(buildState(startedAt, tracked, finalTriggered, finalTriggeredAt)); err != nil {
			emit(eventStateErr, err.Error(), logrus.Fields{"error": err.Error()})
		}
		if pi := cfg.Root.Notification.ProgressInterval.Duration; pi > 0 && time.Since(lastProgressAt) >= pi {
			emit(eventNotifyProgress, fmt.Sprintf("sending progress report elapsed=%s", time.Since(startedAt).Truncate(time.Second)), nil)
			if err := notifyMarkdownInChunks(context.Background(), wecom, notify.EventProgressReport, toRows(tracked, cfg.Root.Connection.GitHubOrg), notifyRowsPerMessage, func(chunk []tui.Row, page, total int) string {
				return buildProgressMarkdown(chunk, startedAt, time.Now().UTC(), page, total)
			}); err != nil {
				emit(eventNotifyErr, err.Error(), logrus.Fields{"error": err.Error()})
			}
			lastProgressAt = time.Now()
		}
		renderer.Render(toRows(tracked, cfg.Root.Connection.GitHubOrg))
		return false, nil
	}

	if done, err := processTick(); err != nil {
		return err
	} else if done {
		return nil
	}

	for {
		select {
		case <-ctx.Done():
			emit(eventTimeout, "global timeout reached or context cancelled", logrus.Fields{"reason": ctx.Err().Error()})
			markTimeout(tracked)
			_ = store.Save(buildState(startedAt, tracked, finalTriggered, finalTriggeredAt))
			return ctx.Err()
		case <-sigCh:
			emit(eventExit, "received stop signal, saving state", nil)
			return store.Save(buildState(startedAt, tracked, finalTriggered, finalTriggeredAt))
		case <-ticker.C:
			if done, err := processTick(); err != nil {
				return err
			} else if done {
				return nil
			}
		}
	}
}

func resolvePatternComponents(ctx context.Context, cfg config.RuntimeConfig, ghc *gh.Client) (config.RuntimeConfig, error) {
	hasPattern := false
	for _, c := range cfg.Components {
		if len(c.BranchPatterns) > 0 {
			hasPattern = true
			break
		}
	}
	if !hasPattern {
		return cfg, nil
	}

	expanded := make([]config.LoadedComponent, 0, len(cfg.Components))
	seen := map[string]struct{}{}
	branchCache := map[string][]string{}
	for _, c := range cfg.Components {
		if len(c.BranchPatterns) == 0 {
			if _, ok := seen[c.Name]; ok {
				return cfg, fmt.Errorf("duplicated runtime component name %q after branch pattern expansion", c.Name)
			}
			seen[c.Name] = struct{}{}
			expanded = append(expanded, c)
			continue
		}

		patterns := make([]*regexp.Regexp, 0, len(c.BranchPatterns))
		for _, raw := range c.BranchPatterns {
			re, err := regexp.Compile(strings.TrimSpace(raw))
			if err != nil {
				return cfg, fmt.Errorf("compile branch pattern %q for component %q: %w", raw, c.Name, err)
			}
			patterns = append(patterns, re)
		}

		branches, ok := branchCache[c.Repo]
		if !ok {
			list, err := ghc.ListBranches(ctx, c.Repo)
			if err != nil {
				return cfg, fmt.Errorf("list branches for %s: %w", c.Repo, err)
			}
			branches = list
			branchCache[c.Repo] = list
		}

		matched := make([]string, 0, len(branches))
		for _, branch := range branches {
			for _, re := range patterns {
				if re.MatchString(branch) {
					matched = append(matched, branch)
					break
				}
			}
		}
		if len(matched) == 0 {
			return cfg, fmt.Errorf("component %q branch_patterns matched no branches in repo %q", c.Name, c.Repo)
		}
		sort.Strings(matched)

		baseName := runtimeComponentBaseName(c)
		for _, branch := range matched {
			name := fmt.Sprintf("%s@%s", baseName, branch)
			if _, ok := seen[name]; ok {
				return cfg, fmt.Errorf("duplicated runtime component name %q after branch pattern expansion", name)
			}
			seen[name] = struct{}{}
			expanded = append(expanded, config.LoadedComponent{
				Name:      name,
				Repo:      c.Repo,
				Branch:    branch,
				Pipelines: c.Pipelines,
			})
		}
	}

	cfg.Components = expanded
	return cfg, nil
}

type branchLister interface {
	ListBranches(context.Context, string) ([]string, error)
}

func shouldSkipPatternResolutionForAdHocScope(components []config.LoadedComponent, opts watchOptions) bool {
	componentName := strings.TrimSpace(opts.componentName)
	pipelineName := strings.TrimSpace(opts.pipelineName)
	if componentName == "" || pipelineName == "" {
		return false
	}
	selected := matchComponentsBySelector(components, componentName)
	return len(selected) == 0
}

func applyWatchScope(cfg *config.RuntimeConfig, opts watchOptions) (bool, error) {
	return applyWatchScopeWithBranchLister(context.Background(), cfg, opts, nil)
}

func applyWatchScopeWithBranchLister(ctx context.Context, cfg *config.RuntimeConfig, opts watchOptions, lister branchLister) (bool, error) {
	componentName := strings.TrimSpace(opts.componentName)
	pipelineName := strings.TrimSpace(opts.pipelineName)
	branch := strings.TrimSpace(opts.branch)
	branchPattern := strings.TrimSpace(opts.branchPattern)

	if branch != "" && branchPattern != "" {
		return false, fmt.Errorf("--branch and --branch-pattern are mutually exclusive")
	}

	var branchRegex *regexp.Regexp
	if branchPattern != "" {
		re, err := regexp.Compile(branchPattern)
		if err != nil {
			return false, fmt.Errorf("compile --branch-pattern %q: %w", branchPattern, err)
		}
		branchRegex = re
	}

	if componentName == "" {
		if pipelineName != "" {
			return false, fmt.Errorf("--pipeline requires --component")
		}
		if branch != "" {
			return false, fmt.Errorf("--branch requires --component")
		}
		if branchPattern != "" {
			return false, fmt.Errorf("--branch-pattern requires --component")
		}
		return false, nil
	}

	selected := matchComponentsBySelector(cfg.Components, componentName)
	if len(selected) == 0 {
		if pipelineName == "" {
			return false, fmt.Errorf("component %q not found", componentName)
		}
		if branchPattern != "" {
			if lister == nil {
				return false, fmt.Errorf("--branch-pattern requires branch lister when component %q is not defined in config", componentName)
			}
			branches, err := lister.ListBranches(ctx, componentName)
			if err != nil {
				return false, fmt.Errorf("list branches for %s: %w", componentName, err)
			}
			matched := make([]string, 0, len(branches))
			for _, current := range branches {
				if branchRegex.MatchString(current) {
					matched = append(matched, current)
				}
			}
			if len(matched) == 0 {
				return false, fmt.Errorf("branch pattern %q matched no branches under component %q", branchPattern, componentName)
			}
			sort.Strings(matched)
			selected = make([]config.LoadedComponent, 0, len(matched))
			multi := len(matched) > 1
			for _, current := range matched {
				adHoc := buildAdHocComponent(componentName, pipelineName, current)
				if multi {
					adHoc.Name = fmt.Sprintf("%s@%s", componentName, current)
				}
				selected = append(selected, adHoc)
			}
		} else {
			selected = []config.LoadedComponent{buildAdHocComponent(componentName, pipelineName, branch)}
		}
	}

	if branch != "" {
		filteredByBranch := make([]config.LoadedComponent, 0, len(selected))
		for _, c := range selected {
			if c.Branch == branch {
				filteredByBranch = append(filteredByBranch, c)
			}
		}
		if len(filteredByBranch) > 0 {
			selected = filteredByBranch
		} else if len(selected) == 1 {
			selected[0].Branch = branch
		} else {
			return false, fmt.Errorf("branch %q not found under component %q", branch, componentName)
		}
	}

	if branchRegex != nil {
		filteredByPattern := make([]config.LoadedComponent, 0, len(selected))
		for _, c := range selected {
			if branchRegex.MatchString(c.Branch) {
				filteredByPattern = append(filteredByPattern, c)
			}
		}
		if len(filteredByPattern) == 0 {
			return false, fmt.Errorf("branch pattern %q matched no branches under component %q", branchPattern, componentName)
		}
		selected = filteredByPattern
	}

	if pipelineName != "" {
		found := false
		for i := range selected {
			filtered := make([]config.PipelineSpec, 0, 1)
			for _, p := range selected[i].Pipelines {
				if p.Name == pipelineName {
					filtered = append(filtered, p)
				}
			}
			if len(filtered) > 0 {
				found = true
			}
			selected[i].Pipelines = filtered
		}
		if !found {
			return false, fmt.Errorf("pipeline %q not found under component %q", pipelineName, componentName)
		}
	}

	cfg.Components = selected
	return true, nil
}

func runtimeComponentBaseName(c config.LoadedComponent) string {
	suffix := "@" + strings.TrimSpace(c.Branch)
	if strings.HasSuffix(c.Name, suffix) {
		return strings.TrimSuffix(c.Name, suffix)
	}
	return c.Name
}

func expandRuntimeDependencies(components []config.LoadedComponent, raw map[string]config.Depends) map[string]config.Depends {
	out := map[string]config.Depends{}
	if len(components) == 0 {
		return out
	}
	byBase := map[string][]string{}
	for _, c := range components {
		base := runtimeComponentBaseName(c)
		byBase[base] = append(byBase[base], c.Name)
	}

	for _, c := range components {
		base := runtimeComponentBaseName(c)
		spec, ok := raw[base]
		if !ok {
			out[c.Name] = config.Depends{}
			continue
		}
		seen := map[string]struct{}{}
		dependsOn := make([]string, 0, len(spec.DependsOn))
		for _, depBase := range spec.DependsOn {
			targets := byBase[depBase]
			if len(targets) == 0 {
				targets = []string{depBase}
			}
			for _, target := range targets {
				if _, ok := seen[target]; ok {
					continue
				}
				seen[target] = struct{}{}
				dependsOn = append(dependsOn, target)
			}
		}
		out[c.Name] = config.Depends{DependsOn: dependsOn}
	}
	return out
}

func watchOnce(ctx context.Context, log *logrus.Logger, cfg config.RuntimeConfig, ghc *gh.Client, dag *resolver.DAG, tracked map[string]*trackedComponent, mode probeMode, dryRun bool, emit func(kind watchEventKind, msg string, fields logrus.Fields)) error {
	succeeded := succeededMap(tracked)
	log.WithFields(logrus.Fields{
		"components": fmt.Sprintf("%d", len(tracked)),
		"dry_run":    fmt.Sprintf("%t", dryRun),
	}).Debug("watch tick start")
	for _, c := range tracked {
		if !dag.IsReady(c.Name, succeeded) {
			log.WithFields(logrus.Fields{
				"component": c.Name,
				"branch":    c.Branch,
			}).Debug("component blocked by dependency DAG")
			for _, p := range c.Pipelines {
				if !isTerminal(p.Status) {
					p.Status = pipestatus.StatusPending
				}
			}
			continue
		}

		for _, p := range c.Pipelines {
			// Backoff state: waiting for the timer to fire before triggering retry.
			if p.Status == pipestatus.StatusBackoff {
				if p.RetryAfter != nil && !time.Now().Before(*p.RetryAfter) {
					sha, err := ghc.BranchSHA(ctx, c.Repo, c.Branch)
					if err != nil {
						return fmt.Errorf("refresh branch sha for %s: %w", c.Name, err)
					}
					c.SHA = sha
					body := strings.ReplaceAll(p.RetryCmd, "{branch}", c.Branch)
					attempt := p.RetryCount + 1
					if dryRun {
						emit(eventDryRetry, fmt.Sprintf("would comment on %s@%s: %s", c.Repo, sha[:8], body), logrus.Fields{"component": c.Name, "branch": c.Branch, "pipeline": p.Name, "sha": sha, "attempt": fmt.Sprintf("%d", attempt)})
					} else {
						if err := ghc.CreateCommitComment(ctx, c.Repo, sha, body); err != nil {
							return fmt.Errorf("trigger retry for %s/%s: %w", c.Name, p.Name, err)
						}
					}
					settleAfter := time.Now().Add(cfg.Root.Retry.RetrySettleDelay.Duration)
					p.SettleAfter = &settleAfter
					p.RetryAfter = nil
					p.Status = pipestatus.StatusSettling
				}
				continue
			}

			// Settling state: waiting for the new PipelineRun to be created.
			if p.Status == pipestatus.StatusSettling {
				if p.SettleAfter != nil && !time.Now().Before(*p.SettleAfter) {
					ns, run, err := retrier.RediscoverPipelineRun(ctx, ghc, c.Repo, c.SHA, p.Name)
					if err != nil {
						return fmt.Errorf("rediscover pipeline run for %s/%s: %w", c.Name, p.Name, err)
					}
					p.Namespace = ns
					p.PipelineRun = run
					p.RetryCount++
					p.Status = pipestatus.StatusWatching
					p.Conclusion = pipestatus.ConclusionUnknown
					p.SettleAfter = nil
					emit(eventRetryOK, fmt.Sprintf("%s/%s new_run=%s", c.Name, p.Name, run), logrus.Fields{"component": c.Name, "branch": c.Branch, "pipeline": p.Name, "sha": c.SHA, "attempt": fmt.Sprintf("%d", p.RetryCount), "reason": "rediscovered"})
				}
				continue
			}

			if useKubectlProbe(mode) && (p.Namespace == "" || p.PipelineRun == "") {
				p.Status = pipestatus.StatusWatching
				p.Conclusion = pipestatus.ConclusionUnknown
				continue
			}

			var (
				res      watcher.ProbeResult
				probeErr error
			)
			if useKubectlProbe(mode) {
				probeCtx, cancel := context.WithTimeout(ctx, probePipelineTimeout)
				res, probeErr = watcher.ProbePipelineRun(probeCtx, p.Namespace, p.PipelineRun, cfg.Root.Connection.Kubeconfig, cfg.Root.Connection.Context)
				cancel()
				if probeErr != nil {
					log.WithFields(logrus.Fields{
						"component": c.Name,
						"pipeline":  p.Name,
						"run":       p.PipelineRun,
						"namespace": p.Namespace,
						"error":     probeErr.Error(),
					}).Debug("probe failed, trying github fallback")
					fallback, source, err := fallbackProbeFromGH(ctx, ghc, c, p.Name, p.PipelineRun)
					if err == nil {
						res = fallback
						probeErr = nil
						checksURL := commitChecksURL(cfg.Root.Connection.GitHubOrg, c.Repo, c.SHA)
						emit(eventGHFallback, ghFallbackEventMessage(cfg.Root.Connection.GitHubOrg, c.Repo, c.SHA), logrus.Fields{
							"component": c.Name,
							"branch":    c.Branch,
							"pipeline":  p.Name,
							"run":       p.PipelineRun,
							"sha":       c.SHA,
							"reason":    source,
							"checks":    checksURL,
						})
					} else {
						log.WithFields(logrus.Fields{
							"component": c.Name,
							"pipeline":  p.Name,
							"run":       p.PipelineRun,
							"sha":       c.SHA,
							"error":     err.Error(),
						}).Debug("github fallback failed")
					}
				}
			} else {
				fallback, _, err := fallbackProbeFromGH(ctx, ghc, c, p.Name, p.PipelineRun)
				if err == nil {
					res = fallback
				} else {
					probeErr = err
					log.WithFields(logrus.Fields{
						"component": c.Name,
						"pipeline":  p.Name,
						"run":       p.PipelineRun,
						"sha":       c.SHA,
						"error":     err.Error(),
					}).Debug("gh-only probe failed")
				}
			}

			status, nextErrs := watcher.DeriveStatusFromProbe(probeErr, res, p.QueryErrors, defaultQueryErrorThreshold)
			p.QueryErrors = nextErrs
			if status == pipestatus.StatusRunning && res.Reason == "gh_fallback_run_mismatch" {
				p.RunMismatch++
				emit(eventRunMismatch, fmt.Sprintf("%s/%s run mismatch (%d/%d), current_run=%s", c.Name, p.Name, p.RunMismatch, runMismatchRetryThreshold, p.PipelineRun), logrus.Fields{
					"component": c.Name,
					"branch":    c.Branch,
					"pipeline":  p.Name,
					"run":       p.PipelineRun,
					"reason":    "gh_fallback_run_mismatch",
				})
				if p.RunMismatch >= runMismatchRetryThreshold {
					status = pipestatus.StatusFailed
					res.Reason = "gh_fallback_run_mismatch"
					p.RunMismatch = 0
					emit(eventRunStale, fmt.Sprintf("%s/%s run mismatch persisted, force retry", c.Name, p.Name), logrus.Fields{
						"component": c.Name,
						"branch":    c.Branch,
						"pipeline":  p.Name,
						"run":       p.PipelineRun,
						"reason":    "gh_fallback_run_mismatch_threshold",
					})
				}
			} else {
				p.RunMismatch = 0
			}

			switch status {
			case pipestatus.StatusSucceeded:
				if p.Status != pipestatus.StatusSucceeded {
					now := time.Now().UTC()
					p.CompletedAt = &now
					if sha, err := ghc.BranchSHA(ctx, c.Repo, c.Branch); err == nil {
						c.SHA = sha
					}
					emit(eventSuccess, fmt.Sprintf("%s/%s", c.Name, p.Name), logrus.Fields{"component": c.Name, "branch": c.Branch, "pipeline": p.Name, "sha": c.SHA})
				}
				p.Status = pipestatus.StatusSucceeded
				p.Conclusion = pipestatus.ConclusionSuccess

			case pipestatus.StatusRunning, pipestatus.StatusWatching:
				p.Status = status
				if probeErr != nil {
					emit(eventQueryWarn, fmt.Sprintf("%s/%s query failed (%d): %v", c.Name, p.Name, p.QueryErrors, probeErr), logrus.Fields{"component": c.Name, "branch": c.Branch, "pipeline": p.Name, "error": probeErr.Error()})
				}

			case pipestatus.StatusQueryErr:
				p.Status = pipestatus.StatusQueryErr
				emit(eventQueryErr, fmt.Sprintf("%s/%s exceeded query error threshold", c.Name, p.Name), logrus.Fields{"component": c.Name, "branch": c.Branch, "pipeline": p.Name, "reason": "query_error_threshold"})

			case pipestatus.StatusFailed:
				p.Conclusion = pipestatus.ConclusionFailure
				retryLimitEnabled := cfg.Root.Retry.MaxRetries > 0
				if retryLimitEnabled && p.RetryCount >= cfg.Root.Retry.MaxRetries {
					if p.Status != pipestatus.StatusExhausted {
						emit(eventExhausted, fmt.Sprintf("%s/%s retries exhausted", c.Name, p.Name), logrus.Fields{"component": c.Name, "branch": c.Branch, "pipeline": p.Name, "sha": c.SHA, "attempt": fmt.Sprintf("%d", p.RetryCount)})
					}
					p.Status = pipestatus.StatusExhausted
					continue
				}
				p.Status = pipestatus.StatusFailed
				emit(eventFailed, fmt.Sprintf("%s/%s failed, retry_count=%d", c.Name, p.Name, p.RetryCount), logrus.Fields{"component": c.Name, "branch": c.Branch, "pipeline": p.Name, "sha": c.SHA, "reason": res.Reason})

				attempt := p.RetryCount + 1
				backoff := retrier.BackoffDuration(
					cfg.Root.Retry.Backoff.Initial.Duration,
					cfg.Root.Retry.Backoff.Multiplier,
					cfg.Root.Retry.Backoff.Max.Duration,
					attempt,
				)
				emit(eventRetrying, fmt.Sprintf("%s/%s attempt=%d backoff=%s", c.Name, p.Name, attempt, backoff), logrus.Fields{"component": c.Name, "branch": c.Branch, "pipeline": p.Name, "sha": c.SHA, "attempt": fmt.Sprintf("%d", attempt)})
				retryAfter := time.Now().Add(backoff)
				p.RetryAfter = &retryAfter
				p.Status = pipestatus.StatusBackoff
			}
		}
	}
	return nil
}

func toTracked(cfg config.RuntimeConfig, rs []component.RuntimeComponent) (map[string]*trackedComponent, error) {
	byName := map[string]*trackedComponent{}
	for _, r := range rs {
		byName[r.Name] = &trackedComponent{
			Name:      r.Name,
			Repo:      r.Repo,
			Branch:    r.Branch,
			SHA:       r.SHA,
			Pipelines: map[string]*trackedPipeline{},
		}
		for _, p := range r.Pipelines {
			byName[r.Name].Pipelines[p.Name] = &trackedPipeline{
				Name:        p.Name,
				Status:      pipestatus.Status(p.Status),
				Conclusion:  pipestatus.Conclusion(p.Conclusion),
				Namespace:   p.Namespace,
				PipelineRun: p.PipelineRun,
			}
		}
	}

	for _, c := range cfg.Components {
		tc, ok := byName[c.Name]
		if !ok {
			return nil, fmt.Errorf("runtime component %q not found", c.Name)
		}
		for _, p := range c.Pipelines {
			tp, ok := tc.Pipelines[p.Name]
			if !ok {
				tp = &trackedPipeline{Name: p.Name, Status: pipestatus.StatusMissing, Conclusion: "-"}
				tc.Pipelines[p.Name] = tp
			}
			tp.RetryCmd = p.RetryCommand
		}
	}

	return byName, nil
}

func buildState(startedAt time.Time, tracked map[string]*trackedComponent, finalTriggered bool, finalTriggeredAt *time.Time) state.File {
	now := time.Now().UTC()
	out := state.File{
		Version:    1,
		StartedAt:  startedAt,
		UpdatedAt:  now,
		Components: map[string]state.Component{},
		FinalAction: state.FinalActionState{
			Triggered:   finalTriggered,
			TriggeredAt: finalTriggeredAt,
		},
	}
	for _, c := range tracked {
		sc := state.Component{
			Branch:    c.Branch,
			SHA:       c.SHA,
			Namespace: firstNamespace(c.Pipelines),
			Pipelines: map[string]state.PipelineState{},
		}
		for _, p := range c.Pipelines {
			sc.Pipelines[p.Name] = state.PipelineState{
				Status:      p.Status,
				PipelineRun: p.PipelineRun,
				RetryCount:  p.RetryCount,
				CompletedAt: p.CompletedAt,
				RetryAfter:  p.RetryAfter,
				SettleAfter: p.SettleAfter,
			}
		}
		out.Components[c.Name] = sc
	}
	return out
}

func isTerminal(status pipestatus.Status) bool {
	switch status {
	case pipestatus.StatusSucceeded, pipestatus.StatusExhausted, pipestatus.StatusBlocked, pipestatus.StatusTimeout:
		return true
	default:
		return false
	}
}

func succeededMap(tracked map[string]*trackedComponent) map[string]bool {
	out := map[string]bool{}
	for _, c := range tracked {
		ok := true
		for _, p := range c.Pipelines {
			if p.Status != pipestatus.StatusSucceeded {
				ok = false
				break
			}
		}
		out[c.Name] = ok
	}
	return out
}

func allComponentsSucceeded(tracked map[string]*trackedComponent) bool {
	for _, ok := range succeededMap(tracked) {
		if !ok {
			return false
		}
	}
	return true
}

func resolveFinalBranch(cliBranch string, cfg config.RuntimeConfig, tracked map[string]*trackedComponent) (string, error) {
	if strings.TrimSpace(cliBranch) != "" {
		return cliBranch, nil
	}
	if strings.TrimSpace(cfg.Root.FinalAction.Branch) != "" {
		return cfg.Root.FinalAction.Branch, nil
	}
	if strings.TrimSpace(cfg.Root.FinalAction.BranchFromComponent) != "" {
		name := cfg.Root.FinalAction.BranchFromComponent
		c, ok := tracked[name]
		if !ok {
			return "", fmt.Errorf("final_action.branch_from_component %q not found", name)
		}
		return c.Branch, nil
	}
	return "", fmt.Errorf("final_action branch is empty")
}

func triggerFinalAction(ctx context.Context, ghc *gh.Client, cfg config.RuntimeConfig, branch string) error {
	repo := cfg.Root.FinalAction.Repo
	if repo == "" {
		return fmt.Errorf("final_action.repo is required")
	}
	sha, err := ghc.BranchSHA(ctx, repo, branch)
	if err != nil {
		return fmt.Errorf("final action branch sha: %w", err)
	}
	body := strings.ReplaceAll(cfg.Root.FinalAction.Comment, "{branch}", branch)
	if err := ghc.CreateCommitComment(ctx, repo, sha, body); err != nil {
		return fmt.Errorf("final action comment failed: %w", err)
	}
	return nil
}

func markTimeout(tracked map[string]*trackedComponent) {
	for _, c := range tracked {
		for _, p := range c.Pipelines {
			if isTerminal(p.Status) {
				continue
			}
			p.Status = pipestatus.StatusTimeout
		}
	}
}

func recoverFromState(ctx context.Context, log *logrus.Logger, mode probeMode, tracked map[string]*trackedComponent, loaded state.File, kubeconfig, kubeContext string) {
	for name, sc := range loaded.Components {
		tc, ok := tracked[name]
		if !ok {
			continue
		}
		stateBranch := strings.TrimSpace(sc.Branch)
		currentBranch := strings.TrimSpace(tc.Branch)
		if stateBranch != "" && currentBranch != "" && stateBranch != currentBranch {
			logEvent(log, "RECOVER_SKIP", "state branch mismatch, skip component recovery", logrus.Fields{
				"component":    tc.Name,
				"state_branch": stateBranch,
				"branch":       currentBranch,
			})
			continue
		}
		if strings.TrimSpace(sc.SHA) != "" {
			tc.SHA = sc.SHA
		}
		for pname, sp := range sc.Pipelines {
			tp, ok := tc.Pipelines[pname]
			if !ok {
				continue
			}

			if strings.TrimSpace(sc.Namespace) != "" {
				tp.Namespace = sc.Namespace
			}
			if strings.TrimSpace(sp.PipelineRun) != "" {
				tp.PipelineRun = component.NormalizePipelineRunName(tp.Name, strings.TrimSpace(sp.PipelineRun))
			}

			if useKubectlProbe(mode) && tp.PipelineRun != "" && tp.Namespace != "" {
				probeCtx, cancel := context.WithTimeout(ctx, recoverProbeTimeout)
				_, err := watcher.ProbePipelineRun(probeCtx, tp.Namespace, tp.PipelineRun, kubeconfig, kubeContext)
				cancel()
				if err == nil {
					// Keep the recovered namespace/run and continue.
				} else {
					logEvent(log, "RECOVER_WARN", "recover probe failed, keep state namespace/pipelinerun", logrus.Fields{
						"component": tc.Name,
						"pipeline":  tp.Name,
						"run":       tp.PipelineRun,
						"namespace": tp.Namespace,
						"error":     err.Error(),
					})
				}
			}

			tp.RetryCount = sp.RetryCount
			tp.Status = sp.Status
			tp.CompletedAt = sp.CompletedAt
			tp.RetryAfter = sp.RetryAfter
			tp.SettleAfter = sp.SettleAfter
		}
	}
}

func firstNamespace(pipelines map[string]*trackedPipeline) string {
	for _, p := range pipelines {
		if p.Namespace != "" {
			return p.Namespace
		}
	}
	return ""
}

func toRows(tracked map[string]*trackedComponent, org string) []tui.Row {
	rows := []tui.Row{}
	for _, c := range tracked {
		for _, p := range c.Pipelines {
			rows = append(rows, tui.Row{
				Component: c.Name,
				Branch:    c.Branch,
				Pipeline:  p.Name,
				Status:    p.Status,
				Retries:   p.RetryCount,
				Run:       normalize(p.PipelineRun),
				CommitURL: commitChecksURL(org, c.Repo, c.SHA),
				BranchURL: branchCommitsURL(org, c.Repo, c.Branch),
			})
		}
	}
	return rows
}

func emitCommitURLsOnce(cfg config.RuntimeConfig, tracked map[string]*trackedComponent, emit func(kind watchEventKind, msg string, fields logrus.Fields)) {
	names := make([]string, 0, len(tracked))
	for name := range tracked {
		names = append(names, name)
	}
	sort.Strings(names)

	org := strings.TrimSpace(cfg.Root.Connection.GitHubOrg)
	for _, name := range names {
		c := tracked[name]
		if c == nil || strings.TrimSpace(c.SHA) == "" || strings.TrimSpace(c.Repo) == "" || org == "" {
			continue
		}
		url := commitChecksURL(org, c.Repo, c.SHA)
		emit(eventCommitURL, fmt.Sprintf("%s checks: %s", c.Name, url), logrus.Fields{
			"component": c.Name,
			"branch":    c.Branch,
			"repo":      c.Repo,
			"sha":       c.SHA,
		})
	}
}

func scopedSuccessBranch(tracked map[string]*trackedComponent) string {
	names := make([]string, 0, len(tracked))
	for name := range tracked {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		c := tracked[name]
		if c == nil {
			continue
		}
		if strings.TrimSpace(c.Branch) != "" {
			return c.Branch
		}
	}
	return "scoped"
}

func successSummaryBranch(scopedMode bool, cliFinalBranch string, cfg config.RuntimeConfig, tracked map[string]*trackedComponent, finalActionEnabled bool) string {
	if scopedMode {
		return scopedSuccessBranch(tracked)
	}
	if finalActionEnabled {
		if branch, err := resolveFinalBranch(cliFinalBranch, cfg, tracked); err == nil && strings.TrimSpace(branch) != "" {
			return branch
		}
	}
	return "multi-branch"
}

func commitChecksURL(org, repo, sha string) string {
	if org == "" || repo == "" || sha == "" {
		return ""
	}
	return fmt.Sprintf("https://github.com/%s/%s/commit/%s/checks", org, repo, sha)
}

func branchCommitsURL(org, repo, branch string) string {
	if org == "" || repo == "" || branch == "" {
		return ""
	}
	return fmt.Sprintf("https://github.com/%s/%s/commits/%s/", org, repo, branch)
}

func ghFallbackEventMessage(org, repo, sha string) string {
	base := "kubectl probe failed, fallback to GH check-run"
	checksURL := commitChecksURL(org, repo, sha)
	if checksURL == "" {
		return base
	}
	return fmt.Sprintf("%s: %s", base, checksURL)
}

func fallbackProbeFromGH(ctx context.Context, ghc *gh.Client, c *trackedComponent, pipeline, currentRun string) (watcher.ProbeResult, string, error) {
	lookup := func(sha string) (watcher.ProbeResult, error) {
		probeWithAnnotations := func(r gh.CheckRun) watcher.ProbeResult {
			base := watcher.ProbeFromCheckRun(r.Status, r.Conclusion)
			if base.Status != pipestatus.StatusSucceeded {
				return base
			}
			if r.ID <= 0 || r.Output.AnnotationsCount <= 0 {
				return base
			}
			annotationsCtx, cancel := context.WithTimeout(ctx, ghAnnotationTimeout)
			defer cancel()
			annotations, err := ghc.CheckRunAnnotations(annotationsCtx, c.Repo, r.ID)
			if err != nil {
				return base
			}
			for _, a := range annotations {
				if strings.EqualFold(strings.TrimSpace(a.AnnotationLevel), "failure") {
					return watcher.ProbeResult{Status: pipestatus.StatusFailed, Reason: "gh_fallback_annotation_failure", Conclusion: pipestatus.ConclusionFailure}
				}
			}
			return base
		}

		runs, err := ghc.CheckRuns(ctx, c.Repo, sha)
		if err != nil {
			return watcher.ProbeResult{}, err
		}
		if strings.TrimSpace(currentRun) != "" {
			if r, ok := component.FindPipelineCheckRunForRun(runs, pipeline, currentRun); ok {
				return probeWithAnnotations(r), nil
			}
			if r, ok := component.FindPipelineCheckRun(runs, pipeline); ok {
				inferred := probeWithAnnotations(r)
				if inferred.Status == pipestatus.StatusFailed {
					return inferred, nil
				}
				return watcher.ProbeResult{Status: pipestatus.StatusRunning, Reason: "gh_fallback_run_mismatch", Conclusion: pipestatus.ConclusionUnknown}, nil
			}
			return watcher.ProbeResult{}, fmt.Errorf("pipeline %q not found in GH check-runs", pipeline)
		}
		if r, ok := component.FindPipelineCheckRun(runs, pipeline); ok {
			return probeWithAnnotations(r), nil
		}
		return watcher.ProbeResult{}, fmt.Errorf("pipeline %q not found in GH check-runs", pipeline)
	}

	if res, err := lookup(c.SHA); err == nil {
		return res, "gh_current_sha", nil
	}
	if strings.TrimSpace(currentRun) != "" {
		return watcher.ProbeResult{}, "", fmt.Errorf("pipeline %q run %q not found on current sha", pipeline, currentRun)
	}

	sha, err := ghc.BranchSHA(ctx, c.Repo, c.Branch)
	if err != nil {
		return watcher.ProbeResult{}, "", err
	}
	if sha != c.SHA {
		c.SHA = sha
	}

	res, err := lookup(c.SHA)
	if err != nil {
		return watcher.ProbeResult{}, "", err
	}
	return res, "gh_refreshed_sha", nil
}

func buildProgressMarkdown(rows []tui.Row, startedAt, reportedAt time.Time, page, total int) string {
	elapsed := reportedAt.Sub(startedAt)
	if elapsed < 0 {
		elapsed = 0
	}
	var sb strings.Builder
	sb.WriteString("## 流水线进度报告\n\n")
	if total > 1 {
		sb.WriteString(fmt.Sprintf("**分片**: %d/%d\n\n", page, total))
	}
	sb.WriteString(fmt.Sprintf("**开始时间**: %s\n\n", formatMarkdownTime(startedAt)))
	sb.WriteString(fmt.Sprintf("**报告时间**: %s\n\n", formatMarkdownTime(reportedAt)))
	sb.WriteString(fmt.Sprintf("**已运行**: %s\n\n", elapsed.Truncate(time.Second)))
	sb.WriteString(tui.MarkdownTable(rows))
	return sb.String()
}

func buildFinalOKMarkdown(branch string, startedAt, finishedAt time.Time, rows []tui.Row, page, total int) string {
	elapsed := finishedAt.Sub(startedAt)
	if elapsed < 0 {
		elapsed = 0
	}
	var sb strings.Builder
	sb.WriteString("## 所有流水线已成功完成\n\n")
	if total > 1 {
		sb.WriteString(fmt.Sprintf("**分片**: %d/%d\n\n", page, total))
	}
	sb.WriteString(fmt.Sprintf("**Branch**: `%s` | **耗时**: %s\n\n", branch, elapsed.Truncate(time.Second)))
	sb.WriteString(fmt.Sprintf("**开始时间**: %s\n\n", formatMarkdownTime(startedAt)))
	sb.WriteString(fmt.Sprintf("**完成时间**: %s\n\n", formatMarkdownTime(finishedAt)))
	sb.WriteString(tui.MarkdownTable(rows))
	return sb.String()
}

func resolveNotifyRowsPerMessage(raw int) int {
	if raw <= 0 {
		return defaultNotifyRowsPerMessage
	}
	return raw
}

func notifyMarkdownInChunks(ctx context.Context, wecom *notify.Wecom, event string, rows []tui.Row, maxRows int, build func(chunk []tui.Row, page, total int) string) error {
	chunks := chunkRowsByConstraints(rows, maxRows, maxNotifyMarkdownBytes, build)
	total := len(chunks)
	for i, chunk := range chunks {
		content := build(chunk, i+1, total)
		notifyCtx, cancel := context.WithTimeout(ctx, notifySendTimeout)
		err := wecom.NotifyMarkdown(notifyCtx, event, content)
		cancel()
		if err != nil {
			return fmt.Errorf("send %s notification part %d/%d: %w", event, i+1, total, err)
		}
		logrus.WithFields(logrus.Fields{
			"event":   event,
			"part":    fmt.Sprintf("%d/%d", i+1, total),
			"rows":    fmt.Sprintf("%d", len(chunk)),
			"bytes":   fmt.Sprintf("%d", len(content)),
			"maxRows": fmt.Sprintf("%d", maxRows),
			"limit":   fmt.Sprintf("%d", maxNotifyMarkdownBytes),
			"enabled": fmt.Sprintf("%t", wecom.Enabled()),
		}).Debug("webhook notification sent")
	}
	return nil
}

func chunkRowsByConstraints(rows []tui.Row, maxRows, maxBytes int, build func(chunk []tui.Row, page, total int) string) [][]tui.Row {
	if len(rows) == 0 {
		return [][]tui.Row{rows}
	}
	if maxRows <= 0 {
		maxRows = len(rows)
	}
	if maxBytes <= 0 {
		maxBytes = maxNotifyMarkdownBytes
	}

	chunks := make([][]tui.Row, 0, (len(rows)+maxRows-1)/maxRows)
	for i := 0; i < len(rows); {
		lastFit := i
		for end := i + 1; end <= len(rows) && (end-i) <= maxRows; end++ {
			candidate := rows[i:end]
			estimate := build(candidate, chunkEstimatePage, chunkEstimatePage)
			if len(estimate) > maxBytes {
				break
			}
			lastFit = end
		}
		if lastFit == i {
			// A single row can still exceed the content limit on very long links.
			// Send it alone and rely on webhook errcode logs for diagnostics.
			lastFit = i + 1
		}
		chunks = append(chunks, rows[i:lastFit])
		i = lastFit
	}
	return chunks
}

func formatMarkdownTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04:05 MST")
}

func logEvent(log *logrus.Logger, kind, msg string, fields logrus.Fields) {
	merged := logrus.Fields{"kind": kind}
	for k, v := range fields {
		merged[k] = v
	}
	log.WithFields(merged).Log(eventLevel(kind), msg)
}

func eventLevel(kind string) logrus.Level {
	k := strings.ToUpper(strings.TrimSpace(kind))
	switch {
	case strings.HasSuffix(k, "_ERR"):
		return logrus.ErrorLevel
	case strings.HasSuffix(k, "_WARN"):
		return logrus.WarnLevel
	case k == string(eventFailed) || k == string(eventExhausted) || k == string(eventTimeout):
		return logrus.ErrorLevel
	case k == string(eventGHFallback) || k == string(eventRunMismatch):
		return logrus.WarnLevel
	default:
		return logrus.InfoLevel
	}
}
