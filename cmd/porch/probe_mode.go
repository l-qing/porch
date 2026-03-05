package main

import (
	"fmt"
	"strings"

	"porch/pkg/config"
)

type probeMode string

const (
	probeModeAuto         probeMode = "auto"
	probeModeGHOnly       probeMode = "gh-only"
	probeModeKubectlFirst probeMode = "kubectl-first"
)

func resolveProbeMode(raw string, cfg config.RuntimeConfig) (probeMode, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", string(probeModeAuto):
		// Auto mode picks kubectl-first only when kube access is configured.
		// Otherwise default to gh-only so CLI still works in local/non-cluster setups.
		if hasKubectlConfig(cfg.Root.Connection.Kubeconfig, cfg.Root.Connection.Context) {
			return probeModeKubectlFirst, nil
		}
		return probeModeGHOnly, nil
	case string(probeModeGHOnly):
		return probeModeGHOnly, nil
	case string(probeModeKubectlFirst), "kubectl":
		return probeModeKubectlFirst, nil
	default:
		return "", fmt.Errorf("invalid probe mode %q, expect auto|gh-only|kubectl-first", raw)
	}
}

func hasKubectlConfig(kubeconfig, kubeContext string) bool {
	return strings.TrimSpace(kubeconfig) != "" || strings.TrimSpace(kubeContext) != ""
}

func useKubectlProbe(mode probeMode) bool {
	return mode == probeModeKubectlFirst
}
