package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

const (
	envPrefix = "PORCH"

	defaultConfigPath = "./orchestrator.yaml"
	defaultStateFile  = ".porch-state.json"
	defaultStateDir   = "porch"

	stateFileSourceDefaultTemp = "default_temp"
	stateFileSourceFlag        = "flag"
	stateFileSourceViper       = "viper"

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

func resolveWatchStateFile(rawFlagValue, rawViperValue string) (path, source string) {
	flagValue := strings.TrimSpace(rawFlagValue)
	if flagValue != "" {
		return flagValue, stateFileSourceFlag
	}
	viperValue := strings.TrimSpace(rawViperValue)
	if viperValue != "" {
		return viperValue, stateFileSourceViper
	}
	return defaultWatchStateFile(), stateFileSourceDefaultTemp
}

func defaultWatchStateFile() string {
	workdir, err := os.Getwd()
	if err != nil {
		return filepath.Join(os.TempDir(), defaultStateDir, defaultStateFile)
	}
	return defaultWatchStateFileForDir(workdir)
}

// defaultWatchStateFileForDir builds a stable temp state path keyed by workdir.
func defaultWatchStateFileForDir(workdir string) string {
	cleanWorkdir := strings.TrimSpace(workdir)
	if cleanWorkdir == "" {
		return filepath.Join(os.TempDir(), defaultStateDir, defaultStateFile)
	}
	if abs, err := filepath.Abs(cleanWorkdir); err == nil {
		cleanWorkdir = abs
	}
	// Hashing keeps the path short and avoids leaking workspace names in tmp file lists.
	sum := sha256.Sum256([]byte(filepath.Clean(cleanWorkdir)))
	key := hex.EncodeToString(sum[:8])
	return filepath.Join(os.TempDir(), defaultStateDir, key, defaultStateFile)
}
