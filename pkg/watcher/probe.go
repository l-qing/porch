package watcher

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	pipestatus "porch/pkg/pipeline"

	"github.com/sirupsen/logrus"
)

type ProbeResult struct {
	Status     pipestatus.Status
	Reason     string
	Conclusion pipestatus.Conclusion
}

func ProbePipelineRun(ctx context.Context, namespace, name, kubeconfig, kubeContext string) (ProbeResult, error) {
	start := time.Now()
	cmdArgs := []string{}
	if kc := resolveKubeconfigPath(kubeconfig); kc != "" {
		cmdArgs = append(cmdArgs, "--kubeconfig", kc)
	}
	if kc := strings.TrimSpace(kubeContext); kc != "" {
		cmdArgs = append(cmdArgs, "--context", kc)
	}
	cmdArgs = append(cmdArgs, "get", "pipelinerun", "-n", namespace, name, "-o", "json")
	cmd := exec.CommandContext(ctx, "kubectl", cmdArgs...)
	logrus.WithFields(logrus.Fields{
		"tool":      "kubectl",
		"namespace": namespace,
		"run":       name,
		"context":   strings.TrimSpace(kubeContext),
		"kubeconfig": func() string {
			if strings.TrimSpace(kubeconfig) == "" {
				return "-"
			}
			return resolveKubeconfigPath(kubeconfig)
		}(),
		"cmd": "kubectl " + strings.Join(maskKubectlArgs(cmdArgs), " "),
	}).Debug("exec external command")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		fields := logrus.Fields{
			"tool":      "kubectl",
			"namespace": namespace,
			"run":       name,
			"context":   strings.TrimSpace(kubeContext),
			"kubeconfig": func() string {
				if strings.TrimSpace(kubeconfig) == "" {
					return "-"
				}
				return resolveKubeconfigPath(kubeconfig)
			}(),
			"cmd":     "kubectl " + strings.Join(maskKubectlArgs(cmdArgs), " "),
			"elapsed": time.Since(start).Truncate(time.Millisecond).String(),
			"error":   err.Error(),
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			fields["stderr"] = summarize(msg, 240)
		}
		logrus.WithFields(fields).Debug("external command failed")
		return ProbeResult{}, err
	}
	logrus.WithFields(logrus.Fields{
		"tool":      "kubectl",
		"namespace": namespace,
		"run":       name,
		"context":   strings.TrimSpace(kubeContext),
		"kubeconfig": func() string {
			if strings.TrimSpace(kubeconfig) == "" {
				return "-"
			}
			return resolveKubeconfigPath(kubeconfig)
		}(),
		"cmd":     "kubectl " + strings.Join(maskKubectlArgs(cmdArgs), " "),
		"elapsed": time.Since(start).Truncate(time.Millisecond).String(),
	}).Debug("external command done")

	payload := struct {
		Status struct {
			Conditions []struct {
				Type   string `json:"type"`
				Status string `json:"status"`
				Reason string `json:"reason"`
			} `json:"conditions"`
		} `json:"status"`
	}{}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		return ProbeResult{}, fmt.Errorf("decode pipelinerun json: %w", err)
	}

	for _, c := range payload.Status.Conditions {
		if c.Type != "Succeeded" {
			continue
		}
		switch c.Status {
		case "True":
			return ProbeResult{Status: pipestatus.StatusSucceeded, Reason: c.Reason, Conclusion: pipestatus.ConclusionSuccess}, nil
		case "False":
			return ProbeResult{Status: pipestatus.StatusFailed, Reason: c.Reason, Conclusion: pipestatus.ConclusionFailure}, nil
		default:
			return ProbeResult{Status: pipestatus.StatusRunning, Reason: c.Reason, Conclusion: pipestatus.ConclusionUnknown}, nil
		}
	}

	return ProbeResult{Status: pipestatus.StatusUnknown, Conclusion: pipestatus.ConclusionUnknown}, nil
}

func summarize(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func resolveKubeconfigPath(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "~/") {
		if u, err := user.Current(); err == nil && strings.TrimSpace(u.HomeDir) != "" {
			return filepath.Join(u.HomeDir, strings.TrimPrefix(s, "~/"))
		}
	}
	return s
}

func maskKubectlArgs(args []string) []string {
	out := make([]string, 0, len(args))
	skip := false
	for i, a := range args {
		if skip {
			skip = false
			continue
		}
		if a == "--kubeconfig" {
			out = append(out, "--kubeconfig", "<path>")
			if i+1 < len(args) {
				skip = true
			}
			continue
		}
		out = append(out, a)
	}
	return out
}
