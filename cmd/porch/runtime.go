package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"porch/pkg/config"

	"github.com/sirupsen/logrus"
)

func loadRuntimeConfig(opts commonOptions) (config.RuntimeConfig, error) {
	return config.LoadWithOptions(opts.configPath, config.LoadOptions{
		ComponentsFileOverride: opts.componentsFile,
		GitHubOrgOverride:      opts.githubOrg,
	})
}

func loadRuntimeConfigForWatch(opts watchOptions) (config.RuntimeConfig, error) {
	cfg, err := loadRuntimeConfig(opts.commonOptions)
	if err == nil {
		return cfg, nil
	}
	if !shouldUseWatchMinimalConfig(opts, err) {
		return cfg, err
	}
	return buildWatchMinimalRuntimeConfig(opts), nil
}

func shouldUseWatchMinimalConfig(opts watchOptions, loadErr error) bool {
	if !errors.Is(loadErr, os.ErrNotExist) {
		return false
	}
	if strings.TrimSpace(opts.githubOrg) == "" {
		return false
	}
	if strings.TrimSpace(opts.componentName) == "" {
		return false
	}
	if strings.TrimSpace(opts.pipelineName) == "" {
		return false
	}
	return true
}

func buildWatchMinimalRuntimeConfig(opts watchOptions) config.RuntimeConfig {
	// Minimal mode is for one-off scoped runs without orchestrator yaml.
	// Keep defaults aligned with README examples for predictable behavior.
	return config.RuntimeConfig{
		Root: config.Root{
			APIVersion: "porch/v1",
			Kind:       "ReleaseOrchestration",
			Connection: config.Connection{
				GitHubOrg: strings.TrimSpace(opts.githubOrg),
			},
			Watch: config.Watch{
				Interval: config.Duration{Duration: 30 * time.Second},
			},
			Retry: config.Retry{
				MaxRetries:       5,
				RetrySettleDelay: config.Duration{Duration: 90 * time.Second},
				Backoff: config.Backoff{
					Initial:    config.Duration{Duration: time.Minute},
					Multiplier: 1.5,
					Max:        config.Duration{Duration: 5 * time.Minute},
				},
			},
			Timeout: config.Timeout{
				Global: config.Duration{Duration: 12 * time.Hour},
			},
			Notification: config.Notification{
				NotifyRowsPerMessage: 12,
			},
			Log: config.Log{
				Level: "info",
			},
			ComponentsFile: "",
			Components:     []config.ComponentSpec{},
			Dependencies:   map[string]config.Depends{},
		},
		Components: []config.LoadedComponent{},
	}
}

func initLogger(cfg config.RuntimeConfig, opts commonOptions) (*logrus.Logger, func() error, error) {
	level := strings.TrimSpace(cfg.Root.Log.Level)
	if strings.TrimSpace(opts.logLevel) != "" {
		level = strings.TrimSpace(opts.logLevel)
	}
	if opts.verbose {
		level = "debug"
	}

	log := logrus.New()
	log.SetFormatter(&logrus.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: "2006-01-02T15:04:05Z07:00",
	})

	lv, err := parseLogrusLevel(level)
	if err != nil {
		return nil, nil, fmt.Errorf("init logger: %w", err)
	}
	log.SetLevel(lv)

	var f *os.File
	if strings.TrimSpace(cfg.Root.Log.File) != "" {
		f, err = os.OpenFile(cfg.Root.Log.File, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, nil, fmt.Errorf("init logger: %w", err)
		}
		log.SetOutput(io.MultiWriter(os.Stdout, f))
	}

	// Keep package-level debug traces (gh/kubectl wrappers) aligned with CLI logger.
	std := logrus.StandardLogger()
	std.SetFormatter(log.Formatter)
	std.SetOutput(log.Out)
	std.SetLevel(log.GetLevel())

	return log, func() error {
		if f == nil {
			return nil
		}
		return f.Close()
	}, nil
}

func parseLogrusLevel(raw string) (logrus.Level, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "info":
		return logrus.InfoLevel, nil
	case "debug":
		return logrus.DebugLevel, nil
	case "warn", "warning":
		return logrus.WarnLevel, nil
	case "error":
		return logrus.ErrorLevel, nil
	default:
		return logrus.InfoLevel, fmt.Errorf("invalid log level %q", raw)
	}
}
