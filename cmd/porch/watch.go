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
	Name             string
	RetryCmd         string
	Namespace        string
	PipelineRun      string
	Status           pipestatus.Status
	Conclusion       pipestatus.Conclusion
	StartedAt        *time.Time
	LastTransitionAt *time.Time
	RetryCount       int
	QueryErrors      int
	RunMismatch      int
	CompletedAt      *time.Time
	RetryAfter       *time.Time
	SettleAfter      *time.Time
}

type trackedComponent struct {
	Name      string
	Repo      string
	Branch    string
	SHA       string
	PRNumber  int
	Pipelines map[string]*trackedPipeline
}

type watchOptions struct {
	commonOptions
	stateFile      string
	stateFileSrc   string
	finalBranch    string
	exitAfterFinal bool
	componentName  string
	pipelineName   string
	branch         string
	tag            string
	branchPattern  string
	prs            string
	dryRun         bool
}

type watchEventKind string

const (
	eventRecoverSkip    watchEventKind = "RECOVER_SKIP"
	eventRecover        watchEventKind = "RECOVER"
	eventRecoverWarn    watchEventKind = "RECOVER_WARN"
	eventStateFile      watchEventKind = "STATE_FILE"
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
	eventHeadUpdate     watchEventKind = "HEAD_UPDATE"
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

const (
	ghFallbackReasonRunMismatch   = "gh_fallback_run_mismatch"
	ghFallbackReasonRuntimeMissed = "gh_fallback_runtime_missing"
)

var errWatchStopRequested = errors.New("watch stop requested")

func newWatchCmd() *cobra.Command {
	opts := watchOptions{}
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Continuously watch pipelines and auto-retry",
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.complete(viperKeyWatchConfig)
			opts.stateFile, opts.stateFileSrc = resolveWatchStateFile(
				opts.stateFile,
				viper.GetString(viperKeyWatchStateFile),
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
	cmd.Flags().StringVar(&opts.tag, "tag", "", "override selected component ref by tag at runtime")
	cmd.Flags().StringVar(&opts.branchPattern, "branch-pattern", "", "select branches by regular expression under selected component")
	cmd.Flags().StringVar(&opts.prs, "prs", "", "comma-separated pull request numbers, e.g. 123,456")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "query only, do not trigger retry")
	mustBindPFlag(viperKeyWatchConfig, cmd, "config")
	mustBindPFlag(viperKeyWatchStateFile, cmd, "state-file")
	mustBindPFlag(viperKeyWatchExitAfterDone, cmd, "exit-after-final-ok")
	return cmd
}

func runWatch(opts watchOptions) error {
	cfg, err := loadRuntimeConfigForWatch(opts)
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
	stopRequested := false
	pollStopSignal := func() bool {
		if stopRequested {
			return true
		}
		select {
		case <-sigCh:
			stopRequested = true
			return true
		default:
			return false
		}
	}

	ghc := gh.NewClient(cfg.Root.Connection.GitHubOrg, nil)
	prNumbers, err := parsePRNumbers(opts.prs)
	if err != nil {
		return err
	}
	var scopedMode bool
	if len(prNumbers) > 0 {
		scopedMode, err = prepareWatchPRMode(ctx, &cfg, opts, prNumbers, ghc)
	} else {
		scopedMode, err = prepareWatchBranchMode(ctx, &cfg, opts, ghc, log)
	}
	if err != nil {
		return err
	}
	notifyRowsPerMessage := resolveNotifyRowsPerMessage(cfg.Root.Notification.NotifyRowsPerMessage)
	log.WithFields(logrus.Fields{
		"config":               opts.configPath,
		"components_file":      cfg.Root.ComponentsFile,
		"state_file":           opts.stateFile,
		"state_file_source":    opts.stateFileSrc,
		"component_scope":      normalize(strings.TrimSpace(opts.componentName)),
		"pipeline_scope":       normalize(strings.TrimSpace(opts.pipelineName)),
		"branch_override":      normalize(strings.TrimSpace(opts.branch)),
		"tag_scope":            normalize(strings.TrimSpace(opts.tag)),
		"branch_pattern_scope": normalize(strings.TrimSpace(opts.branchPattern)),
		"prs":                  normalize(strings.TrimSpace(opts.prs)),
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
	stateFileMsg := fmt.Sprintf("state file path=%s (source=%s)", opts.stateFile, normalize(opts.stateFileSrc))
	emit(eventStateFile, stateFileMsg, logrus.Fields{
		"state_file":        opts.stateFile,
		"state_file_source": normalize(opts.stateFileSrc),
	})
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
	onceDeps := watchOnceDeps{
		log:        log,
		cfg:        cfg,
		ghc:        ghc,
		dag:        dag,
		mode:       mode,
		dryRun:     opts.dryRun,
		shouldStop: pollStopSignal,
		emit:       emit,
	}
	processTick := func() (bool, error) {
		if err := watchOnce(ctx, tracked, onceDeps); err != nil {
			if errors.Is(err, errWatchStopRequested) {
				emit(eventExit, "received stop signal, saving state", nil)
				return true, store.Save(buildState(startedAt, tracked, finalTriggered, finalTriggeredAt))
			}
			emit(eventWatchErr, err.Error(), logrus.Fields{"error": err.Error()})
		}
		if !firstCheckDone {
			firstCheckDone = true
			if !allComponentsSucceeded(tracked) {
				lastProgressAt = time.Time{}
			}
		}

		allSucceeded := allComponentsSucceeded(tracked)
		rows := toRows(tracked, cfg.Root.Connection)
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
			if err := notifyMarkdownInChunks(context.Background(), wecom, notify.EventProgressReport, toRows(tracked, cfg.Root.Connection), notifyRowsPerMessage, func(chunk []tui.Row, page, total int) string {
				return buildProgressMarkdown(chunk, startedAt, time.Now().UTC(), page, total)
			}); err != nil {
				emit(eventNotifyErr, err.Error(), logrus.Fields{"error": err.Error()})
			}
			lastProgressAt = time.Now()
		}
		renderer.Render(toRows(tracked, cfg.Root.Connection))
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
			if stopRequested {
				emit(eventExit, "received stop signal, saving state", nil)
				return store.Save(buildState(startedAt, tracked, finalTriggered, finalTriggeredAt))
			}
			emit(eventTimeout, "global timeout reached or context cancelled", logrus.Fields{"reason": ctx.Err().Error()})
			markTimeout(tracked)
			_ = store.Save(buildState(startedAt, tracked, finalTriggered, finalTriggeredAt))
			return ctx.Err()
		case <-sigCh:
			stopRequested = true
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
			c.Pipelines = config.NormalizePipelineSpecs(c.Pipelines)
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
				Pipelines: config.NormalizePipelineSpecs(c.Pipelines),
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
	tag := strings.TrimSpace(opts.tag)
	if componentName != "" && tag != "" {
		return true
	}
	if componentName == "" || pipelineName == "" {
		return false
	}
	selected := matchComponentsBySelector(components, componentName)
	return len(selected) == 0
}

func selectComponentsForPatternResolution(components []config.LoadedComponent, opts watchOptions) ([]config.LoadedComponent, bool) {
	componentName := strings.TrimSpace(opts.componentName)
	if componentName == "" {
		return components, false
	}
	if strings.TrimSpace(opts.tag) != "" {
		// Tag-scoped watch resolves by explicit ref and does not need branch listing.
		return components, true
	}
	selected := matchComponentsBySelector(components, componentName)
	if len(selected) == 0 {
		return components, true
	}
	return selected, false
}

func prepareWatchPRMode(ctx context.Context, cfg *config.RuntimeConfig, opts watchOptions, prs []int, ghc *gh.Client) (bool, error) {
	// PR mode binds runtime targets to explicit PR numbers and uses PR head refs as branches.
	if strings.TrimSpace(opts.componentName) == "" {
		return false, fmt.Errorf("--prs requires --component")
	}
	if strings.TrimSpace(opts.branch) != "" {
		return false, fmt.Errorf("--prs and --branch are mutually exclusive")
	}
	if strings.TrimSpace(opts.tag) != "" {
		return false, fmt.Errorf("--prs and --tag are mutually exclusive")
	}
	if strings.TrimSpace(opts.branchPattern) != "" {
		return false, fmt.Errorf("--prs and --branch-pattern are mutually exclusive")
	}
	if err := applyWatchPRScope(ctx, cfg, strings.TrimSpace(opts.componentName), strings.TrimSpace(opts.pipelineName), prs, ghc); err != nil {
		return false, err
	}
	return true, nil
}

func prepareWatchBranchMode(ctx context.Context, cfg *config.RuntimeConfig, opts watchOptions, ghc *gh.Client, log *logrus.Logger) (bool, error) {
	componentsForPattern, skipPatternResolution := selectComponentsForPatternResolution(cfg.Components, opts)
	if skipPatternResolution {
		log.WithFields(logrus.Fields{
			"component_scope": normalize(strings.TrimSpace(opts.componentName)),
			"pipeline_scope":  normalize(strings.TrimSpace(opts.pipelineName)),
		}).Debug("skip global branch pattern expansion for unresolved scoped watch")
	} else {
		updated := *cfg
		updated.Components = componentsForPattern
		resolved, err := resolvePatternComponents(ctx, updated, ghc)
		if err != nil {
			return false, err
		}
		cfg.Components = resolved.Components
	}
	return applyWatchScopeWithBranchLister(ctx, cfg, opts, ghc)
}

func applyWatchScope(cfg *config.RuntimeConfig, opts watchOptions) (bool, error) {
	return applyWatchScopeWithBranchLister(context.Background(), cfg, opts, nil)
}

func applyWatchPRScope(ctx context.Context, cfg *config.RuntimeConfig, componentName, pipelineName string, prs []int, ghc *gh.Client) error {
	// The --pipeline flag accepts "<name> [key=value ...]"; the bare name drives
	// check-run matching while extraArgs are forwarded into the /test comment.
	pipelineName, extraArgs := config.SplitPipelineArg(pipelineName)
	selected := matchComponentsBySelector(cfg.Components, componentName)
	if len(selected) == 0 {
		if pipelineName == "" {
			return fmt.Errorf("component %q not found", componentName)
		}
		// Ad-hoc mode uses component selector as repo name and requires pipeline to build retry command.
		selected = []config.LoadedComponent{buildAdHocComponent(componentName, pipelineName, extraArgs, "")}
	}
	repo := strings.TrimSpace(selected[0].Repo)
	if repo == "" {
		return fmt.Errorf("component %q repo is empty", componentName)
	}
	for _, c := range selected[1:] {
		if strings.TrimSpace(c.Repo) != repo {
			return fmt.Errorf("component %q maps to multiple repos in config", componentName)
		}
	}
	base := selected[0]
	if pipelineName != "" {
		filtered := make([]config.PipelineSpec, 0, 1)
		for _, p := range base.Pipelines {
			if p.Name == pipelineName {
				filtered = append(filtered, normalizePipelineSpecForScope(p, extraArgs))
			}
		}
		if len(filtered) == 0 {
			// Allow one-off scoped execution even when pipeline is not pre-declared in config.
			filtered = append(filtered, normalizePipelineSpecForScope(config.PipelineSpec{Name: pipelineName}, extraArgs))
		}
		base.Pipelines = filtered
	}

	expanded := make([]config.LoadedComponent, 0, len(prs))
	seenNames := map[string]struct{}{}
	baseName := runtimeComponentBaseName(base)
	for _, number := range prs {
		pr, err := ghc.PullRequest(ctx, repo, number)
		if err != nil {
			return err
		}
		if strings.TrimSpace(pr.State) != "open" {
			return fmt.Errorf("pull request %s/%s#%d is not open (state=%s)", cfg.Root.Connection.GitHubOrg, repo, number, pr.State)
		}
		name := fmt.Sprintf("%s#%d", baseName, pr.Number)
		if _, ok := seenNames[name]; ok {
			return fmt.Errorf("duplicated runtime component name %q in --prs expansion", name)
		}
		seenNames[name] = struct{}{}
		expanded = append(expanded, config.LoadedComponent{
			Name:      name,
			Repo:      repo,
			Branch:    strings.TrimSpace(pr.Head.Ref),
			Pipelines: base.Pipelines,
			PRNumber:  pr.Number,
		})
	}
	cfg.Components = expanded
	return nil
}

func applyWatchScopeWithBranchLister(ctx context.Context, cfg *config.RuntimeConfig, opts watchOptions, lister branchLister) (bool, error) {
	componentName := strings.TrimSpace(opts.componentName)
	// --pipeline value may carry PAC-style extra args (e.g. "name key=value");
	// only the bare name is used for matching, args are appended to retry comments.
	pipelineName, pipelineExtraArgs := config.SplitPipelineArg(opts.pipelineName)
	branch := strings.TrimSpace(opts.branch)
	tag := strings.TrimSpace(opts.tag)
	branchPattern := strings.TrimSpace(opts.branchPattern)

	if branch != "" && branchPattern != "" {
		return false, fmt.Errorf("--branch and --branch-pattern are mutually exclusive")
	}
	if branch != "" && tag != "" {
		return false, fmt.Errorf("--branch and --tag are mutually exclusive")
	}
	if tag != "" && branchPattern != "" {
		return false, fmt.Errorf("--tag and --branch-pattern are mutually exclusive")
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
		if tag != "" {
			return false, fmt.Errorf("--tag requires --component")
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
				adHoc := buildAdHocComponent(componentName, pipelineName, pipelineExtraArgs, current)
				if multi {
					adHoc.Name = fmt.Sprintf("%s@%s", componentName, current)
				}
				selected = append(selected, adHoc)
			}
		} else {
			ref := firstNonEmpty(tag, branch)
			selected = []config.LoadedComponent{buildAdHocComponent(componentName, pipelineName, pipelineExtraArgs, ref)}
		}
	}
	if tag != "" {
		// Tag scope always resolves to a single runtime component ref.
		selected = []config.LoadedComponent{withRuntimeComponentRef(selected[0], tag)}
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
		for i := range selected {
			filtered := make([]config.PipelineSpec, 0, 1)
			for _, p := range selected[i].Pipelines {
				if p.Name == pipelineName {
					filtered = append(filtered, normalizePipelineSpecForScope(p, pipelineExtraArgs))
				}
			}
			if len(filtered) == 0 {
				// Keep scoped runs lightweight: synthesize a default retry command from CLI args.
				filtered = append(filtered, normalizePipelineSpecForScope(config.PipelineSpec{Name: pipelineName}, pipelineExtraArgs))
			}
			selected[i].Pipelines = filtered
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

func withRuntimeComponentRef(c config.LoadedComponent, ref string) config.LoadedComponent {
	updated := c
	normalizedRef := strings.TrimSpace(ref)
	updated.Branch = normalizedRef
	baseName := runtimeComponentBaseName(c)
	if normalizedRef == "" {
		updated.Name = baseName
	} else {
		updated.Name = fmt.Sprintf("%s@%s", baseName, normalizedRef)
	}
	return updated
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

type watchOnceDeps struct {
	log        *logrus.Logger
	cfg        config.RuntimeConfig
	ghc        *gh.Client
	dag        *resolver.DAG
	mode       probeMode
	dryRun     bool
	shouldStop func() bool
	emit       func(kind watchEventKind, msg string, fields logrus.Fields)
}

func watchOnce(ctx context.Context, tracked map[string]*trackedComponent, deps watchOnceDeps) error {
	log := deps.log
	cfg := deps.cfg
	ghc := deps.ghc
	dag := deps.dag
	mode := deps.mode
	dryRun := deps.dryRun
	emit := deps.emit
	isStopping := func() bool {
		if deps.shouldStop != nil && deps.shouldStop() {
			return true
		}
		return false
	}

	if err := syncSucceededComponentsToLatestHead(ctx, tracked, ghc, cfg.Root.Connection.GitHubOrg, log, emit, isStopping); err != nil {
		return err
	}

	succeeded := succeededMap(tracked)
	log.WithFields(logrus.Fields{
		"components": fmt.Sprintf("%d", len(tracked)),
		"dry_run":    fmt.Sprintf("%t", dryRun),
	}).Debug("watch tick start")
	for _, c := range tracked {
		if isStopping() {
			return errWatchStopRequested
		}
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
			if isStopping() {
				return errWatchStopRequested
			}
			// Backoff state: waiting for the timer to fire before triggering retry.
			if p.Status == pipestatus.StatusBackoff {
				if p.RetryAfter != nil && !time.Now().Before(*p.RetryAfter) {
					if err := triggerRetry(ctx, ghc, cfg, c, p, dryRun, emit); err != nil {
						if isStopping() {
							return errWatchStopRequested
						}
						return fmt.Errorf("trigger retry for %s/%s: %w", c.Name, p.Name, err)
					}
				}
				continue
			}

			// Settling state: waiting for the new PipelineRun to be created.
			if p.Status == pipestatus.StatusSettling {
				if p.SettleAfter != nil && !time.Now().Before(*p.SettleAfter) {
					ns, run, err := retrier.RediscoverPipelineRun(ctx, ghc, c.Repo, c.SHA, p.Name)
					if err != nil {
						if isStopping() {
							return errWatchStopRequested
						}
						return fmt.Errorf("rediscover pipeline run for %s/%s: %w", c.Name, p.Name, err)
					}
					p.Namespace = ns
					p.PipelineRun = run
					p.RetryCount++
					p.Status = pipestatus.StatusWatching
					p.Conclusion = pipestatus.ConclusionUnknown
					p.StartedAt = nil
					p.CompletedAt = nil
					p.LastTransitionAt = nil
					p.SettleAfter = nil
					emit(eventRetryOK, fmt.Sprintf("%s/%s new_run=%s", c.Name, p.Name, run), logrus.Fields{"component": c.Name, "branch": c.Branch, "pipeline": p.Name, "sha": c.SHA, "attempt": fmt.Sprintf("%d", p.RetryCount), "reason": "rediscovered"})
				}
				continue
			}

			var (
				res      watcher.ProbeResult
				probeErr error
			)
			if useKubectlProbe(mode) {
				// Some check-runs do not expose a parseable PipelineRun in details_url.
				// In that case we cannot probe via kubectl and should fall back to GH
				// instead of keeping the pipeline in RUN forever.
				if strings.TrimSpace(p.Namespace) == "" || strings.TrimSpace(p.PipelineRun) == "" {
					fallback, source, err := fallbackProbeFromGH(ctx, ghc, c, p.Name, p.PipelineRun)
					if err == nil {
						res = fallback
						probeErr = nil
						discoveredNS, discoveredRun, discoveredSource, discoverErr := discoverRuntimeLocationFromGH(ctx, ghc, c, p.Name)
						if discoverErr == nil {
							if strings.TrimSpace(p.Namespace) == "" && strings.TrimSpace(discoveredNS) != "" {
								p.Namespace = discoveredNS
							}
							if strings.TrimSpace(p.PipelineRun) == "" && strings.TrimSpace(discoveredRun) != "" {
								p.PipelineRun = discoveredRun
							}
							log.WithFields(logrus.Fields{
								"component": c.Name,
								"pipeline":  p.Name,
								"namespace": normalize(p.Namespace),
								"run":       normalize(p.PipelineRun),
								"sha":       c.SHA,
								"reason":    discoveredSource,
							}).Debug("discovered runtime location from github check-run")
						} else {
							log.WithFields(logrus.Fields{
								"component": c.Name,
								"pipeline":  p.Name,
								"sha":       c.SHA,
								"error":     discoverErr.Error(),
							}).Debug("failed to discover runtime location from github check-run")
						}
						checksURL := commitChecksURL(cfg.Root.Connection.GitHubOrg, c.Repo, c.SHA)
						msg := "pipeline run missing, fallback to GH check-run"
						if checksURL != "" {
							msg = fmt.Sprintf("%s: %s", msg, checksURL)
						}
						emit(eventGHFallback, msg, logrus.Fields{
							"component": c.Name,
							"branch":    c.Branch,
							"pipeline":  p.Name,
							"run":       p.PipelineRun,
							"sha":       c.SHA,
							"reason":    source,
							"checks":    checksURL,
						})
					} else {
						probeErr = err
						log.WithFields(logrus.Fields{
							"component": c.Name,
							"pipeline":  p.Name,
							"run":       p.PipelineRun,
							"namespace": p.Namespace,
							"sha":       c.SHA,
							"error":     err.Error(),
						}).Debug("github fallback failed when runtime run is missing")
					}
				} else {
					probeCtx, cancel := context.WithTimeout(ctx, probePipelineTimeout)
					res, probeErr = watcher.ProbePipelineRun(probeCtx, p.Namespace, p.PipelineRun, cfg.Root.Connection.Kubeconfig, cfg.Root.Connection.Context)
					cancel()
				}
				// Only retry GH fallback here for real kubectl probe failures.
				// Missing run/namespace has already been handled in the block above.
				if probeErr != nil && strings.TrimSpace(p.Namespace) != "" && strings.TrimSpace(p.PipelineRun) != "" {
					kubectlProbeErr := probeErr
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
						// If the tracked PipelineRun disappears from cluster while GH still reports
						// a non-terminal check-run, keep retrying with a bounded threshold.
						if isPipelineRunNotFoundError(kubectlProbeErr) &&
							(fallback.Status == pipestatus.StatusRunning || fallback.Status == pipestatus.StatusWatching) {
							res.Reason = ghFallbackReasonRuntimeMissed
						}
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
			if shouldRefreshPRStatusFromGH(c, mode, probeErr, res) {
				ghRes, source, err := fallbackProbeFromGH(ctx, ghc, c, p.Name, p.PipelineRun)
				if err != nil {
					log.WithFields(logrus.Fields{
						"component": c.Name,
						"pipeline":  p.Name,
						"run":       p.PipelineRun,
						"sha":       c.SHA,
						"error":     err.Error(),
					}).Debug("pr authoritative probe from github failed")
				} else if shouldOverrideProbeResultWithPRStatus(res, ghRes) {
					previous := res
					res = ghRes
					emit(eventGHFallback, fmt.Sprintf("%s/%s PR mode prefer GH status (%s->%s)", c.Name, p.Name, previous.Status, ghRes.Status), logrus.Fields{
						"component": c.Name,
						"branch":    c.Branch,
						"pipeline":  p.Name,
						"run":       p.PipelineRun,
						"sha":       c.SHA,
						"reason":    "pr_authoritative_" + source,
					})
				}
			}

			status, nextErrs := watcher.DeriveStatusFromProbe(probeErr, res, p.QueryErrors, defaultQueryErrorThreshold)
			if isStopping() {
				return errWatchStopRequested
			}
			if probeErr == nil {
				if res.StartedAt != nil {
					p.StartedAt = cloneTimePtr(res.StartedAt)
				}
				if res.LastTransitionAt != nil {
					p.LastTransitionAt = cloneTimePtr(res.LastTransitionAt)
				}
				if res.CompletedAt != nil {
					p.CompletedAt = cloneTimePtr(res.CompletedAt)
				}
			}
			p.QueryErrors = nextErrs
			if status == pipestatus.StatusRunning &&
				(res.Reason == ghFallbackReasonRunMismatch || res.Reason == ghFallbackReasonRuntimeMissed) {
				p.RunMismatch++
				staleKind := "run mismatch"
				if res.Reason == ghFallbackReasonRuntimeMissed {
					staleKind = "runtime missing"
				}
				emit(eventRunMismatch, fmt.Sprintf("%s/%s %s (%d/%d), current_run=%s", c.Name, p.Name, staleKind, p.RunMismatch, runMismatchRetryThreshold, p.PipelineRun), logrus.Fields{
					"component": c.Name,
					"branch":    c.Branch,
					"pipeline":  p.Name,
					"run":       p.PipelineRun,
					"reason":    res.Reason,
				})
				if p.RunMismatch >= runMismatchRetryThreshold {
					status = pipestatus.StatusFailed
					thresholdReason := res.Reason
					p.RunMismatch = 0
					emit(eventRunStale, fmt.Sprintf("%s/%s %s persisted, force retry", c.Name, p.Name, staleKind), logrus.Fields{
						"component": c.Name,
						"branch":    c.Branch,
						"pipeline":  p.Name,
						"run":       p.PipelineRun,
						"reason":    thresholdReason + "_threshold",
					})
				}
			} else {
				p.RunMismatch = 0
			}

			switch status {
			case pipestatus.StatusSucceeded:
				if p.Status != pipestatus.StatusSucceeded {
					if p.CompletedAt == nil {
						now := time.Now().UTC()
						p.CompletedAt = &now
					}
					if sha, err := refreshRuntimeSHA(ctx, ghc, c); err == nil {
						c.SHA = sha
					}
					emit(eventSuccess, fmt.Sprintf("%s/%s", c.Name, p.Name), logrus.Fields{"component": c.Name, "branch": c.Branch, "pipeline": p.Name, "sha": c.SHA})
				}
				p.Status = pipestatus.StatusSucceeded
				p.Conclusion = pipestatus.ConclusionSuccess

			case pipestatus.StatusRunning, pipestatus.StatusWatching:
				p.Status = status
				p.CompletedAt = nil
				if probeErr != nil {
					emit(eventQueryWarn, fmt.Sprintf("%s/%s query failed (%d): %v", c.Name, p.Name, p.QueryErrors, probeErr), logrus.Fields{"component": c.Name, "branch": c.Branch, "pipeline": p.Name, "error": probeErr.Error()})
				}

			case pipestatus.StatusQueryErr:
				emit(eventQueryErr, fmt.Sprintf("%s/%s exceeded query error threshold", c.Name, p.Name), logrus.Fields{"component": c.Name, "branch": c.Branch, "pipeline": p.Name, "reason": "query_error_threshold"})
				// Query error threshold means status cannot be trusted anymore.
				// Escalate to failed path so retry policy can recover automatically.
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
				if p.CompletedAt == nil && p.LastTransitionAt != nil {
					p.CompletedAt = cloneTimePtr(p.LastTransitionAt)
				}
				emit(eventFailed, fmt.Sprintf("%s/%s failed, retry_count=%d", c.Name, p.Name, p.RetryCount), logrus.Fields{"component": c.Name, "branch": c.Branch, "pipeline": p.Name, "sha": c.SHA, "reason": "query_error_threshold"})

				attempt := p.RetryCount + 1
				backoff := retrier.BackoffDuration(
					cfg.Root.Retry.Backoff.Initial.Duration,
					cfg.Root.Retry.Backoff.Multiplier,
					cfg.Root.Retry.Backoff.Max.Duration,
					attempt,
				)
				if shouldRetryImmediatelyOnFirstFailure(p) {
					emit(eventRetrying, fmt.Sprintf("%s/%s attempt=%d backoff=0s", c.Name, p.Name, attempt), logrus.Fields{"component": c.Name, "branch": c.Branch, "pipeline": p.Name, "sha": c.SHA, "attempt": fmt.Sprintf("%d", attempt), "reason": "first_failure_fast_path"})
					if err := triggerRetry(ctx, ghc, cfg, c, p, dryRun, emit); err != nil {
						if isStopping() {
							return errWatchStopRequested
						}
						return fmt.Errorf("trigger retry for %s/%s: %w", c.Name, p.Name, err)
					}
					continue
				}
				emit(eventRetrying, fmt.Sprintf("%s/%s attempt=%d backoff=%s", c.Name, p.Name, attempt, backoff), logrus.Fields{"component": c.Name, "branch": c.Branch, "pipeline": p.Name, "sha": c.SHA, "attempt": fmt.Sprintf("%d", attempt)})
				retryAfter := time.Now().Add(backoff)
				p.RetryAfter = &retryAfter
				p.Status = pipestatus.StatusBackoff

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
				if p.CompletedAt == nil && p.LastTransitionAt != nil {
					p.CompletedAt = cloneTimePtr(p.LastTransitionAt)
				}
				emit(eventFailed, fmt.Sprintf("%s/%s failed, retry_count=%d", c.Name, p.Name, p.RetryCount), logrus.Fields{"component": c.Name, "branch": c.Branch, "pipeline": p.Name, "sha": c.SHA, "reason": res.Reason})

				attempt := p.RetryCount + 1
				backoff := retrier.BackoffDuration(
					cfg.Root.Retry.Backoff.Initial.Duration,
					cfg.Root.Retry.Backoff.Multiplier,
					cfg.Root.Retry.Backoff.Max.Duration,
					attempt,
				)
				if shouldRetryImmediatelyOnFirstFailure(p) {
					emit(eventRetrying, fmt.Sprintf("%s/%s attempt=%d backoff=0s", c.Name, p.Name, attempt), logrus.Fields{"component": c.Name, "branch": c.Branch, "pipeline": p.Name, "sha": c.SHA, "attempt": fmt.Sprintf("%d", attempt), "reason": "first_failure_fast_path"})
					if err := triggerRetry(ctx, ghc, cfg, c, p, dryRun, emit); err != nil {
						if isStopping() {
							return errWatchStopRequested
						}
						return fmt.Errorf("trigger retry for %s/%s: %w", c.Name, p.Name, err)
					}
					continue
				}
				emit(eventRetrying, fmt.Sprintf("%s/%s attempt=%d backoff=%s", c.Name, p.Name, attempt, backoff), logrus.Fields{"component": c.Name, "branch": c.Branch, "pipeline": p.Name, "sha": c.SHA, "attempt": fmt.Sprintf("%d", attempt)})
				retryAfter := time.Now().Add(backoff)
				p.RetryAfter = &retryAfter
				p.Status = pipestatus.StatusBackoff
			}
		}
	}
	return nil
}

func syncSucceededComponentsToLatestHead(
	ctx context.Context,
	tracked map[string]*trackedComponent,
	ghc *gh.Client,
	org string,
	log *logrus.Logger,
	emit func(kind watchEventKind, msg string, fields logrus.Fields),
	shouldStop func() bool,
) error {
	names := make([]string, 0, len(tracked))
	for name := range tracked {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		if shouldStop != nil && shouldStop() {
			return errWatchStopRequested
		}
		c := tracked[name]
		if c == nil || !componentPipelinesAllSucceeded(c) {
			continue
		}

		oldSHA := strings.TrimSpace(c.SHA)
		oldBranch := strings.TrimSpace(c.Branch)
		changed, err := refreshComponentTrackingOnHeadChange(ctx, ghc, c)
		if err != nil {
			log.WithFields(logrus.Fields{
				"component": c.Name,
				"branch":    normalize(c.Branch),
				"sha":       normalize(c.SHA),
				"error":     err.Error(),
			}).Debug("failed to sync component head")
		}
		if !changed {
			continue
		}

		checksURL := commitChecksURL(org, c.Repo, c.SHA)
		msg := fmt.Sprintf("%s branch head changed %s -> %s", c.Name, shortSHA(oldSHA), shortSHA(c.SHA))
		if checksURL != "" {
			msg = fmt.Sprintf("%s: %s", msg, checksURL)
		}
		emit(eventHeadUpdate, msg, logrus.Fields{
			"component":  c.Name,
			"repo":       c.Repo,
			"old_branch": normalize(oldBranch),
			"branch":     normalize(c.Branch),
			"old_sha":    normalize(oldSHA),
			"sha":        normalize(c.SHA),
			"checks":     checksURL,
		})
	}

	return nil
}

func componentPipelinesAllSucceeded(c *trackedComponent) bool {
	if c == nil || len(c.Pipelines) == 0 {
		return false
	}
	for _, p := range c.Pipelines {
		if p == nil || p.Status != pipestatus.StatusSucceeded {
			return false
		}
	}
	return true
}

func refreshComponentTrackingOnHeadChange(ctx context.Context, ghc *gh.Client, c *trackedComponent) (bool, error) {
	if c == nil || ghc == nil {
		return false, nil
	}

	oldSHA := strings.TrimSpace(c.SHA)
	sha, err := refreshRuntimeSHA(ctx, ghc, c)
	if err != nil {
		return false, err
	}
	sha = strings.TrimSpace(sha)
	if sha == "" || sha == oldSHA {
		return false, nil
	}

	// Head moved: drop stale run state and rebuild tracking from check-runs of new SHA.
	runs, err := ghc.CheckRuns(ctx, c.Repo, sha)
	applyComponentHeadSnapshot(c, nil)
	if err != nil {
		return true, fmt.Errorf("query check-runs for new head %s: %w", shortSHA(sha), err)
	}
	applyComponentHeadSnapshot(c, runs)
	return true, nil
}

func applyComponentHeadSnapshot(c *trackedComponent, runs []gh.CheckRun) {
	if c == nil {
		return
	}
	for _, p := range c.Pipelines {
		if p == nil {
			continue
		}

		p.RetryCount = 0
		p.QueryErrors = 0
		p.RunMismatch = 0
		p.RetryAfter = nil
		p.SettleAfter = nil
		p.StartedAt = nil
		p.CompletedAt = nil
		p.LastTransitionAt = nil
		p.Namespace = ""
		p.PipelineRun = ""
		p.Conclusion = pipestatus.ConclusionUnknown
		p.Status = pipestatus.StatusWatching

		r, ok := component.FindPipelineCheckRun(runs, p.Name)
		if !ok {
			continue
		}
		probe := watcher.ProbeFromCheckRun(r.Status, r.Conclusion)
		p.Status = probe.Status
		p.Conclusion = probe.Conclusion

		namespace, run, _ := component.ParseDetailsURLForPipeline(r.DetailsURL, p.Name)
		if strings.TrimSpace(run) == "" {
			run = component.PipelineRunFromCheckRun(r, p.Name)
		}
		p.Namespace = strings.TrimSpace(namespace)
		p.PipelineRun = strings.TrimSpace(run)
	}
}

func toTracked(cfg config.RuntimeConfig, rs []component.RuntimeComponent) (map[string]*trackedComponent, error) {
	byName := map[string]*trackedComponent{}
	for _, r := range rs {
		byName[r.Name] = &trackedComponent{
			Name:      r.Name,
			Repo:      r.Repo,
			Branch:    r.Branch,
			SHA:       r.SHA,
			PRNumber:  0,
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
			tp.RetryCmd = config.NormalizePipelineSpec(p).RetryCommand
		}
		tc.PRNumber = c.PRNumber
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

func toRows(tracked map[string]*trackedComponent, conn config.Connection) []tui.Row {
	org := conn.GitHubOrg
	now := time.Now().UTC()
	rows := []tui.Row{}
	for _, c := range tracked {
		for _, p := range c.Pipelines {
			rows = append(rows, tui.Row{
				Component: c.Name,
				Branch:    c.Branch,
				Pipeline:  p.Name,
				Status:    p.Status,
				Retries:   p.RetryCount,
				Elapsed:   pipelineElapsedText(p.StartedAt, p.CompletedAt, p.LastTransitionAt, p.Status, now),
				Run:       normalize(p.PipelineRun),
				RunURL:    pipelineRunDetailURL(p.Namespace, p.PipelineRun, conn),
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

func isPipelineRunNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(msg, "notfound") || strings.Contains(msg, "not found")
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
				return watcher.ProbeResult{Status: pipestatus.StatusRunning, Reason: ghFallbackReasonRunMismatch, Conclusion: pipestatus.ConclusionUnknown}, nil
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

	sha, err := refreshRuntimeSHA(ctx, ghc, c)
	if err != nil {
		return watcher.ProbeResult{}, "", err
	}
	c.SHA = sha

	res, err := lookup(c.SHA)
	if err != nil {
		return watcher.ProbeResult{}, "", err
	}
	return res, "gh_refreshed_sha", nil
}

func discoverRuntimeLocationFromGH(ctx context.Context, ghc *gh.Client, c *trackedComponent, pipeline string) (string, string, string, error) {
	lookup := func(sha string) (string, string, bool, error) {
		runs, err := ghc.CheckRuns(ctx, c.Repo, sha)
		if err != nil {
			return "", "", false, err
		}
		r, ok := component.FindPipelineCheckRun(runs, pipeline)
		if !ok {
			return "", "", false, nil
		}
		namespace, run, _ := component.ParseDetailsURLForPipeline(r.DetailsURL, pipeline)
		if strings.TrimSpace(run) == "" {
			run = component.PipelineRunFromCheckRun(r, pipeline)
		}
		if strings.TrimSpace(run) == "" {
			return "", "", false, nil
		}
		return namespace, run, true, nil
	}

	if namespace, run, ok, err := lookup(c.SHA); err == nil && ok {
		return namespace, run, "gh_location_current_sha", nil
	} else if err != nil {
		return "", "", "", err
	}

	sha, err := refreshRuntimeSHA(ctx, ghc, c)
	if err != nil {
		return "", "", "", err
	}
	c.SHA = sha

	namespace, run, ok, err := lookup(c.SHA)
	if err != nil {
		return "", "", "", err
	}
	if !ok {
		return "", "", "", fmt.Errorf("pipeline %q location not found in GH check-runs", pipeline)
	}
	return namespace, run, "gh_location_refreshed_sha", nil
}

func shouldRefreshPRStatusFromGH(c *trackedComponent, mode probeMode, probeErr error, kubectlResult watcher.ProbeResult) bool {
	if c == nil || c.PRNumber <= 0 {
		return false
	}
	if !useKubectlProbe(mode) || probeErr != nil {
		return false
	}
	// PR mode treats GitHub check-runs as the source of truth for terminal state.
	// Only refresh while kubectl still reports a non-terminal result.
	switch kubectlResult.Status {
	case pipestatus.StatusSucceeded, pipestatus.StatusFailed:
		return false
	default:
		return true
	}
}

func shouldOverrideProbeResultWithPRStatus(current, gh watcher.ProbeResult) bool {
	if gh.Status == pipestatus.StatusFailed {
		// GH failure should override kubectl non-terminal states to avoid hanging on stale RUN.
		return current.Status != pipestatus.StatusFailed || current.Conclusion != pipestatus.ConclusionFailure
	}

	if gh.Status == pipestatus.StatusSucceeded {
		// GH success can be stale (for example older run or child task success).
		// Keep kubectl running/watching as authoritative and only trust GH success
		// when kubectl payload is unknown.
		return current.Status == pipestatus.StatusUnknown
	}

	// Unknown kubectl payloads are less informative than GH running/watching states.
	if current.Status == pipestatus.StatusUnknown {
		return gh.Status == pipestatus.StatusRunning || gh.Status == pipestatus.StatusWatching
	}
	return false
}

func shouldRetryImmediatelyOnFirstFailure(p *trackedPipeline) bool {
	if p == nil {
		return false
	}
	// Fast-path first failure so initial startup can recover without waiting for backoff ticks.
	return p.RetryCount == 0
}

func triggerRetry(
	ctx context.Context,
	ghc *gh.Client,
	cfg config.RuntimeConfig,
	c *trackedComponent,
	p *trackedPipeline,
	dryRun bool,
	emit func(kind watchEventKind, msg string, fields logrus.Fields),
) error {
	sha, err := refreshRuntimeSHA(ctx, ghc, c)
	if err != nil {
		return fmt.Errorf("refresh runtime sha: %w", err)
	}
	c.SHA = sha
	body := strings.ReplaceAll(p.RetryCmd, "{branch}", c.Branch)
	attempt := p.RetryCount + 1
	if dryRun {
		emit(eventDryRetry, fmt.Sprintf("would comment on %s: %s", retryDryTarget(c, sha), body), logrus.Fields{"component": c.Name, "branch": c.Branch, "pr": fmt.Sprintf("%d", c.PRNumber), "pipeline": p.Name, "sha": sha, "attempt": fmt.Sprintf("%d", attempt)})
	} else {
		if err := postRetryComment(ctx, ghc, c, sha, body); err != nil {
			return err
		}
	}
	settleAfter := time.Now().Add(cfg.Root.Retry.RetrySettleDelay.Duration)
	p.SettleAfter = &settleAfter
	p.RetryAfter = nil
	p.Status = pipestatus.StatusSettling
	p.StartedAt = nil
	p.CompletedAt = nil
	p.LastTransitionAt = nil
	return nil
}

func retryDryTarget(c *trackedComponent, sha string) string {
	if c.PRNumber > 0 {
		return fmt.Sprintf("%s#%d", c.Repo, c.PRNumber)
	}
	return fmt.Sprintf("%s@%s", c.Repo, shortSHA(sha))
}

func shortSHA(sha string) string {
	s := strings.TrimSpace(sha)
	if len(s) <= 8 {
		return s
	}
	return s[:8]
}

func postRetryComment(ctx context.Context, ghc *gh.Client, c *trackedComponent, sha, body string) error {
	if c.PRNumber > 0 {
		return postRetryCommentToPR(ctx, ghc, c, body)
	}
	return postRetryCommentToCommit(ctx, ghc, c, sha, body)
}

func postRetryCommentToPR(ctx context.Context, ghc *gh.Client, c *trackedComponent, body string) error {
	if err := ghc.CreatePullRequestComment(ctx, c.Repo, c.PRNumber, body); err != nil {
		return fmt.Errorf("on pull request #%d: %w", c.PRNumber, err)
	}
	return nil
}

func postRetryCommentToCommit(ctx context.Context, ghc *gh.Client, c *trackedComponent, sha, body string) error {
	if err := ghc.CreateCommitComment(ctx, c.Repo, sha, body); err != nil {
		return err
	}
	return nil
}

func refreshRuntimeSHA(ctx context.Context, ghc *gh.Client, c *trackedComponent) (string, error) {
	if c.PRNumber > 0 {
		// PR mode should always follow the pull request head commit, not branch refs.
		pr, err := ghc.PullRequest(ctx, c.Repo, c.PRNumber)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(pr.State) != "open" {
			return "", fmt.Errorf("pull request %s#%d is not open (state=%s)", c.Repo, c.PRNumber, pr.State)
		}
		c.Branch = strings.TrimSpace(pr.Head.Ref)
		sha := strings.TrimSpace(pr.Head.SHA)
		if sha == "" {
			return "", fmt.Errorf("empty pull request head sha for %s#%d", c.Repo, c.PRNumber)
		}
		c.SHA = sha
		return sha, nil
	}
	sha, err := ghc.BranchSHA(ctx, c.Repo, c.Branch)
	if err != nil {
		return "", err
	}
	c.SHA = sha
	return sha, nil
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
	// Ensure chunk boundaries keep the global display priority across parts.
	sortedRows := tui.SortedRowsForDisplay(rows)
	chunks := chunkRowsByConstraints(sortedRows, maxRows, maxNotifyMarkdownBytes, build)
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
