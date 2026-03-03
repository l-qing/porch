package main

import (
	"strings"

	"github.com/spf13/viper"
)

const (
	envPrefix = "PORCH"

	defaultConfigPath = "./orchestrator.yaml"
	defaultStateFile  = "./.porch-state.json"

	viperKeyComponentsFile     = "components_file"
	viperKeyDisableFinalAction = "disable_final_action"
	viperKeyFinalBranch        = "final_branch"
	viperKeyLogLevel           = "log_level"
	viperKeyProbeMode          = "probe_mode"
	viperKeyVerbose            = "verbose"
	viperKeyStatusConfig       = "status.config"
	viperKeyRetryConfig        = "retry.config"
	viperKeyWatchConfig        = "watch.config"
	viperKeyWatchStateFile     = "watch.state_file"
	viperKeyWatchExitAfterDone = "watch.exit_after_final_ok"
)

type commonOptions struct {
	configPath         string
	componentsFile     string
	disableFinalAction bool
	logLevel           string
	probeMode          string
	verbose            bool
}

func (o *commonOptions) complete(configKey string) {
	o.configPath = firstNonEmpty(
		strings.TrimSpace(o.configPath),
		strings.TrimSpace(viper.GetString(configKey)),
		defaultConfigPath,
	)
	o.componentsFile = strings.TrimSpace(firstNonEmpty(
		o.componentsFile,
		viper.GetString(viperKeyComponentsFile),
	))
	if !o.disableFinalAction {
		o.disableFinalAction = viper.GetBool(viperKeyDisableFinalAction)
	}
	o.logLevel = strings.TrimSpace(firstNonEmpty(
		o.logLevel,
		viper.GetString(viperKeyLogLevel),
	))
	o.probeMode = strings.TrimSpace(firstNonEmpty(
		o.probeMode,
		viper.GetString(viperKeyProbeMode),
	))
	if !o.verbose {
		o.verbose = viper.GetBool(viperKeyVerbose)
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
