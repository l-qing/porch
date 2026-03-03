package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"porch/pkg/config"

	"github.com/sirupsen/logrus"
)

func loadRuntimeConfig(opts commonOptions) (config.RuntimeConfig, error) {
	return config.LoadWithOptions(opts.configPath, config.LoadOptions{
		ComponentsFileOverride: opts.componentsFile,
	})
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
