package main

import (
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func init() {
	cobra.OnInitialize(initConfig)
}

func Execute() error {
	return newRootCmd().Execute()
}

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "porch",
		Short:         "Pipeline Orchestrator CLI",
		SilenceUsage:  true,
		SilenceErrors: true,
		Long:          "Porch watches multiple CI pipelines, retries failures, and orchestrates release flow.",
	}

	cmd.PersistentFlags().String("final-branch", "", "override final_action.branch at runtime")
	cmd.PersistentFlags().String("components-file", "", "override components_file at runtime")
	cmd.PersistentFlags().String("github-org", "", "override connection.github_org at runtime")
	cmd.PersistentFlags().Bool("disable-final-action", false, "disable final_action trigger globally")
	cmd.PersistentFlags().String("log-level", "", "override log level (debug|info|warn|error)")
	cmd.PersistentFlags().String("probe-mode", "", "probe mode: auto|gh-only|kubectl-first")
	cmd.PersistentFlags().Bool("verbose", false, "enable verbose debug logs (equivalent to --log-level=debug)")

	mustBindPFlag(viperKeyFinalBranch, cmd, "final-branch")
	mustBindPFlag(viperKeyComponentsFile, cmd, "components-file")
	mustBindPFlag(viperKeyConnectionGitHubOrg, cmd, "github-org")
	mustBindPFlag(viperKeyDisableFinalAction, cmd, "disable-final-action")
	mustBindPFlag(viperKeyLogLevel, cmd, "log-level")
	mustBindPFlag(viperKeyProbeMode, cmd, "probe-mode")
	mustBindPFlag(viperKeyVerbose, cmd, "verbose")

	cmd.AddCommand(newStatusCmd())
	cmd.AddCommand(newRetryCmd())
	cmd.AddCommand(newWatchCmd())

	return cmd
}

func initConfig() {
	viper.SetEnvPrefix(envPrefix)
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_", ".", "_"))
	viper.AutomaticEnv()
}

func mustBindPFlag(key string, cmd *cobra.Command, flagName string) {
	flag := cmd.Flags().Lookup(flagName)
	if flag == nil {
		flag = cmd.PersistentFlags().Lookup(flagName)
	}
	if flag == nil {
		panic("bind flag failed: flag not found: " + flagName)
	}
	if err := viper.BindPFlag(key, flag); err != nil {
		panic("bind flag failed: " + err.Error())
	}
}
