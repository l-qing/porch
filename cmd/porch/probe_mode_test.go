package main

import (
	"testing"

	"porch/pkg/config"
)

func TestResolveProbeMode(t *testing.T) {
	t.Run("auto defaults to gh-only when kube info missing", func(t *testing.T) {
		cfg := config.RuntimeConfig{}
		got, err := resolveProbeMode("", cfg)
		if err != nil {
			t.Fatalf("resolveProbeMode error: %v", err)
		}
		if got != probeModeGHOnly {
			t.Fatalf("mode = %q, want %q", got, probeModeGHOnly)
		}
	})

	t.Run("auto chooses kubectl-first when kube info exists", func(t *testing.T) {
		cfg := config.RuntimeConfig{
			Root: config.Root{
				Connection: config.Connection{Kubeconfig: "~/.kube/config"},
			},
		}
		got, err := resolveProbeMode("auto", cfg)
		if err != nil {
			t.Fatalf("resolveProbeMode error: %v", err)
		}
		if got != probeModeKubectlFirst {
			t.Fatalf("mode = %q, want %q", got, probeModeKubectlFirst)
		}
	})

	t.Run("explicit gh-only overrides kube info", func(t *testing.T) {
		cfg := config.RuntimeConfig{
			Root: config.Root{
				Connection: config.Connection{Kubeconfig: "~/.kube/config"},
			},
		}
		got, err := resolveProbeMode("gh-only", cfg)
		if err != nil {
			t.Fatalf("resolveProbeMode error: %v", err)
		}
		if got != probeModeGHOnly {
			t.Fatalf("mode = %q, want %q", got, probeModeGHOnly)
		}
	})

	t.Run("explicit kubectl alias is accepted", func(t *testing.T) {
		cfg := config.RuntimeConfig{}
		got, err := resolveProbeMode("kubectl", cfg)
		if err != nil {
			t.Fatalf("resolveProbeMode error: %v", err)
		}
		if got != probeModeKubectlFirst {
			t.Fatalf("mode = %q, want %q", got, probeModeKubectlFirst)
		}
	})

	t.Run("invalid mode returns error", func(t *testing.T) {
		cfg := config.RuntimeConfig{}
		if _, err := resolveProbeMode("invalid-mode", cfg); err == nil {
			t.Fatalf("expect error for invalid mode")
		}
	})
}
