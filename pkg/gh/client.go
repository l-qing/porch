package gh

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

type Runner interface {
	Run(ctx context.Context, args ...string) ([]byte, []byte, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

type Client struct {
	org          string
	runner       Runner
	retryBackoff []time.Duration
}

func NewClient(org string, runner Runner) *Client {
	if runner == nil {
		runner = ExecRunner{}
	}
	return &Client{
		org:    org,
		runner: runner,
		// Keep retries short to absorb transient GitHub/GH CLI failures
		// without hiding persistent configuration/auth errors.
		retryBackoff: []time.Duration{300 * time.Millisecond, 1 * time.Second, 2 * time.Second},
	}
}

func (c *Client) run(ctx context.Context, args ...string) ([]byte, []byte, error) {
	start := time.Now()
	safe := sanitizeArgs(args)
	logrus.WithFields(logrus.Fields{
		"tool": "gh",
		"cmd":  "gh " + strings.Join(safe, " "),
	}).Debug("exec external command")

	maxAttempts := len(c.retryBackoff) + 1
	var (
		out    []byte
		errOut []byte
		err    error
	)
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		out, errOut, err = c.runner.Run(ctx, args...)
		if err == nil {
			logrus.WithFields(logrus.Fields{
				"tool":    "gh",
				"cmd":     "gh " + strings.Join(safe, " "),
				"elapsed": time.Since(start).Truncate(time.Millisecond).String(),
				"attempt": fmt.Sprintf("%d/%d", attempt, maxAttempts),
			}).Debug("external command done")
			return out, errOut, nil
		}
		// Retry only transient failures; caller-facing behavior for permanent
		// failures should stay deterministic and fail fast.
		if attempt < maxAttempts && isTransientGHError(errOut, err) && ctx.Err() == nil {
			retryAfter := c.retryBackoff[attempt-1]
			fields := logrus.Fields{
				"tool":        "gh",
				"cmd":         "gh " + strings.Join(safe, " "),
				"elapsed":     time.Since(start).Truncate(time.Millisecond).String(),
				"attempt":     fmt.Sprintf("%d/%d", attempt, maxAttempts),
				"retry_after": retryAfter.String(),
				"error":       err.Error(),
			}
			if msg := strings.TrimSpace(string(errOut)); msg != "" {
				fields["stderr"] = summarize(msg, 240)
			}
			logrus.WithFields(fields).Warn("external command failed, will retry")
			if sleepErr := sleepWithContext(ctx, retryAfter); sleepErr != nil {
				return out, errOut, sleepErr
			}
			continue
		}

		fields := logrus.Fields{
			"tool":    "gh",
			"cmd":     "gh " + strings.Join(safe, " "),
			"elapsed": time.Since(start).Truncate(time.Millisecond).String(),
			"attempt": fmt.Sprintf("%d/%d", attempt, maxAttempts),
			"error":   err.Error(),
		}
		if msg := strings.TrimSpace(string(errOut)); msg != "" {
			fields["stderr"] = summarize(msg, 240)
		}
		logrus.WithFields(fields).Debug("external command failed")
		return out, errOut, err
	}
	return out, errOut, err
}

func (c *Client) BranchSHA(ctx context.Context, repo, branch string) (string, error) {
	path := fmt.Sprintf("repos/%s/%s/commits/%s", c.org, repo, branch)
	out, errOut, err := c.run(ctx, "api", path)
	if err != nil {
		return "", commandError([]string{"gh", "api", path}, errOut, err)
	}

	payload := struct {
		SHA string `json:"sha"`
	}{}
	if err := json.Unmarshal(out, &payload); err != nil {
		return "", fmt.Errorf("decode branch sha response: %w", err)
	}
	if payload.SHA == "" {
		return "", fmt.Errorf("empty sha for %s/%s", repo, branch)
	}
	return payload.SHA, nil
}

func (c *Client) ListBranches(ctx context.Context, repo string) ([]string, error) {
	path := fmt.Sprintf("repos/%s/%s/branches?per_page=100", c.org, repo)
	out, errOut, err := c.run(ctx, "api", "--paginate", path, "--jq", ".[].name")
	if err != nil {
		return nil, commandError([]string{"gh", "api", "--paginate", path, "--jq", ".[].name"}, errOut, err)
	}

	names := make([]string, 0, 32)
	seen := map[string]struct{}{}
	for _, line := range strings.Split(string(out), "\n") {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

type CheckRun struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	DetailsURL string `json:"details_url"`
	ExternalID string `json:"external_id"`
	Output     struct {
		AnnotationsCount int    `json:"annotations_count"`
		AnnotationsURL   string `json:"annotations_url"`
	} `json:"output"`
}

type PullRequest struct {
	Number int    `json:"number"`
	State  string `json:"state"`
	Head   struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
}

func (c *Client) CheckRuns(ctx context.Context, repo, sha string) ([]CheckRun, error) {
	path := fmt.Sprintf("repos/%s/%s/commits/%s/check-runs", c.org, repo, sha)
	out, errOut, err := c.run(ctx, "api", path)
	if err != nil {
		return nil, commandError([]string{"gh", "api", path}, errOut, err)
	}

	payload := struct {
		CheckRuns []CheckRun `json:"check_runs"`
	}{}
	if err := json.Unmarshal(out, &payload); err != nil {
		return nil, fmt.Errorf("decode check-runs response: %w", err)
	}
	return payload.CheckRuns, nil
}

func (c *Client) PullRequest(ctx context.Context, repo string, number int) (PullRequest, error) {
	path := fmt.Sprintf("repos/%s/%s/pulls/%d", c.org, repo, number)
	out, errOut, err := c.run(ctx, "api", path)
	if err != nil {
		return PullRequest{}, commandError([]string{"gh", "api", path}, errOut, err)
	}

	pr := PullRequest{}
	if err := json.Unmarshal(out, &pr); err != nil {
		return PullRequest{}, fmt.Errorf("decode pull request response: %w", err)
	}
	if pr.Number == 0 {
		return PullRequest{}, fmt.Errorf("empty pull request number for %s/%s#%d", c.org, repo, number)
	}
	if strings.TrimSpace(pr.Head.Ref) == "" {
		return PullRequest{}, fmt.Errorf("empty pull request head ref for %s/%s#%d", c.org, repo, number)
	}
	return pr, nil
}

type CheckRunAnnotation struct {
	AnnotationLevel string `json:"annotation_level"`
	Title           string `json:"title"`
	Message         string `json:"message"`
}

func (c *Client) CheckRunAnnotations(ctx context.Context, repo string, checkRunID int64) ([]CheckRunAnnotation, error) {
	path := fmt.Sprintf("repos/%s/%s/check-runs/%d/annotations?per_page=100", c.org, repo, checkRunID)
	out, errOut, err := c.run(ctx, "api", path)
	if err != nil {
		return nil, commandError([]string{"gh", "api", path}, errOut, err)
	}

	annotations := []CheckRunAnnotation{}
	if err := json.Unmarshal(out, &annotations); err != nil {
		return nil, fmt.Errorf("decode check-run annotations response: %w", err)
	}
	return annotations, nil
}

func (c *Client) CreateCommitComment(ctx context.Context, repo, sha, body string) error {
	path := fmt.Sprintf("repos/%s/%s/commits/%s/comments", c.org, repo, sha)
	_, errOut, err := c.run(ctx, "api", path, "-f", "body="+body)
	if err != nil {
		return commandError([]string{"gh", "api", path, "-f", "body=<redacted>"}, errOut, err)
	}
	return nil
}

func (c *Client) CreatePullRequestComment(ctx context.Context, repo string, number int, body string) error {
	path := fmt.Sprintf("repos/%s/%s/issues/%d/comments", c.org, repo, number)
	_, errOut, err := c.run(ctx, "api", path, "-f", "body="+body)
	if err != nil {
		return commandError([]string{"gh", "api", path, "-f", "body=<redacted>"}, errOut, err)
	}
	return nil
}

func sanitizeArgs(args []string) []string {
	safe := make([]string, 0, len(args))
	for _, arg := range args {
		if strings.HasPrefix(arg, "body=") {
			// Avoid leaking trigger commands into logs.
			safe = append(safe, "body=<redacted>")
			continue
		}
		safe = append(safe, arg)
	}
	return safe
}

func summarize(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func commandError(cmd []string, stderr []byte, runErr error) error {
	msg := strings.TrimSpace(string(stderr))
	if msg == "" {
		return fmt.Errorf("command %q failed: %w", strings.Join(cmd, " "), runErr)
	}
	return fmt.Errorf("command %q failed: %v: %s", strings.Join(cmd, " "), runErr, msg)
}

var transientHTTPStatusPattern = regexp.MustCompile(`\bhttp\s+5\d\d\b`)

func isTransientGHError(stderr []byte, runErr error) bool {
	if runErr == nil {
		return false
	}
	if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
		// Context cancellation is a control signal, not a retryable error.
		return false
	}
	text := strings.ToLower(strings.TrimSpace(string(stderr)))
	if errText := strings.ToLower(strings.TrimSpace(runErr.Error())); errText != "" {
		if text != "" {
			text += " "
		}
		text += errText
	}
	if text == "" {
		return false
	}
	if transientHTTPStatusPattern.MatchString(text) {
		return true
	}
	transientTokens := []string{
		"bad gateway",
		"service unavailable",
		"gateway timeout",
		"timeout",
		"timed out",
		"connection reset",
		"connection refused",
		"temporary failure",
		"tls handshake timeout",
		"unexpected eof",
	}
	for _, token := range transientTokens {
		if strings.Contains(text, token) {
			return true
		}
	}
	return false
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
