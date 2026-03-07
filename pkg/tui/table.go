package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	pipestatus "porch/pkg/pipeline"
)

type Row struct {
	Component string
	Branch    string
	Pipeline  string
	Status    pipestatus.Status
	Retries   int
	Elapsed   string
	Run       string
	RunURL    string
	CommitURL string
	BranchURL string
}

type Renderer struct {
	events []string
}

func NewRenderer() *Renderer {
	return &Renderer{}
}

func (r *Renderer) AddEvent(kind, message string) {
	line := fmt.Sprintf("[%s] %-12s %s", time.Now().Format("15:04:05"), kind, message)
	r.events = append(r.events, line)
	if len(r.events) > 12 {
		r.events = r.events[len(r.events)-12:]
	}
}

func (r *Renderer) Render(rows []Row) {
	fmt.Print("\033[H\033[2J")
	fmt.Print(TerminalTable(rows))

	fmt.Println()
	fmt.Println("Events:")
	for _, e := range r.events {
		fmt.Println(e)
	}
}

// TerminalTable renders rows in the same aligned text format used by watch mode,
// including a summary line.
func TerminalTable(rows []Row) string {
	sorted := SortedRowsForDisplay(rows)

	headers := []string{"Component", "Branch", "Pipeline", "Status", "Retries", "Elapsed", "Run", "RunURL"}
	widths := []int{len(headers[0]), len(headers[1]), len(headers[2]), len(headers[3]), len(headers[4]), len(headers[5]), len(headers[6]), len(headers[7])}

	for _, row := range sorted {
		status := renderStatus(row.Status)
		run := strings.TrimSpace(row.Run)
		if run == "" {
			run = "-"
		}
		runURL := strings.TrimSpace(row.RunURL)
		if runURL == "" {
			runURL = "-"
		}
		retries := strconv.Itoa(row.Retries)
		elapsed := strings.TrimSpace(row.Elapsed)
		if elapsed == "" {
			elapsed = "-"
		}

		widths[0] = max(widths[0], len(row.Component))
		widths[1] = max(widths[1], len(row.Branch))
		widths[2] = max(widths[2], len(row.Pipeline))
		widths[3] = max(widths[3], len(status))
		widths[4] = max(widths[4], len(retries))
		widths[5] = max(widths[5], len(elapsed))
		widths[6] = max(widths[6], len(run))
		widths[7] = max(widths[7], len(runURL))
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%-*s  %-*s  %-*s  %-*s  %-*s  %-*s  %-*s  %-*s\n",
		widths[0], headers[0],
		widths[1], headers[1],
		widths[2], headers[2],
		widths[3], headers[3],
		widths[4], headers[4],
		widths[5], headers[5],
		widths[6], headers[6],
		widths[7], headers[7],
	))
	sb.WriteString(fmt.Sprintf("%s  %s  %s  %s  %s  %s  %s  %s\n",
		strings.Repeat("-", widths[0]),
		strings.Repeat("-", widths[1]),
		strings.Repeat("-", widths[2]),
		strings.Repeat("-", widths[3]),
		strings.Repeat("-", widths[4]),
		strings.Repeat("-", widths[5]),
		strings.Repeat("-", widths[6]),
		strings.Repeat("-", widths[7]),
	))
	sb.WriteString(fmt.Sprintf("Summary: succeeded=%d/%d\n\n", countSucceeded(sorted), len(sorted)))

	for _, row := range sorted {
		run := strings.TrimSpace(row.Run)
		if run == "" {
			run = "-"
		}
		runURL := strings.TrimSpace(row.RunURL)
		if runURL == "" {
			runURL = "-"
		}
		elapsed := strings.TrimSpace(row.Elapsed)
		if elapsed == "" {
			elapsed = "-"
		}
		sb.WriteString(fmt.Sprintf("%-*s  %-*s  %-*s  %-*s  %*d  %-*s  %-*s  %-*s\n",
			widths[0], row.Component,
			widths[1], row.Branch,
			widths[2], row.Pipeline,
			widths[3], renderStatus(row.Status),
			widths[4], row.Retries,
			widths[5], elapsed,
			widths[6], run,
			widths[7], runURL,
		))
	}
	return sb.String()
}

func renderStatus(status pipestatus.Status) string {
	switch status {
	case pipestatus.StatusSucceeded:
		return "OK"
	case pipestatus.StatusRunning, pipestatus.StatusWatching:
		return "RUN"
	case pipestatus.StatusFailed:
		return "FAIL"
	case pipestatus.StatusExhausted:
		return "EXHAUSTED"
	case pipestatus.StatusPending:
		return "PENDING"
	case pipestatus.StatusQueryErr:
		return "QUERY_ERR"
	case pipestatus.StatusTimeout:
		return "TIMEOUT"
	default:
		return strings.ToUpper(status.String())
	}
}

func countSucceeded(rows []Row) int {
	n := 0
	for _, r := range rows {
		if r.Status == pipestatus.StatusSucceeded {
			n++
		}
	}
	return n
}

// MarkdownTable renders rows as a summary line and a GFM-style pipe table
// suitable for WeChat Work (企业微信) markdown_v2 webhook messages.
func MarkdownTable(rows []Row) string {
	sorted := SortedRowsForDisplay(rows)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("succeeded=%d/%d\n\n", countSucceeded(sorted), len(sorted)))

	sb.WriteString("| Component | Branch | Pipeline | Status | Retries | Elapsed |\n")
	sb.WriteString("| :--- | :--- | :--- | :---: | :---: | :--- |\n")
	for _, r := range sorted {
		component := r.Component
		if r.CommitURL != "" {
			component = fmt.Sprintf("[%s](%s)", r.Component, r.CommitURL)
		}
		branch := r.Branch
		if r.BranchURL != "" {
			branch = fmt.Sprintf("[%s](%s)", r.Branch, r.BranchURL)
		}
		pipeline := r.Pipeline
		if runURL := strings.TrimSpace(r.RunURL); runURL != "" {
			pipeline = fmt.Sprintf("[%s](%s)", r.Pipeline, runURL)
		}
		elapsed := strings.TrimSpace(r.Elapsed)
		if elapsed == "" {
			elapsed = "-"
		}
		sb.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %d | %s |\n",
			component, branch, pipeline,
			renderStatus(r.Status), r.Retries, elapsed))
	}
	return sb.String()
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// SortedRowsForDisplay returns a sorted copy using the shared TUI/Webhook ordering.
func SortedRowsForDisplay(rows []Row) []Row {
	sorted := make([]Row, len(rows))
	copy(sorted, rows)
	sort.Slice(sorted, func(i, j int) bool {
		return lessDisplayRow(sorted[i], sorted[j])
	})
	return sorted
}

func lessDisplayRow(left, right Row) bool {
	leftRank := statusSortRank(left.Status)
	rightRank := statusSortRank(right.Status)
	if leftRank != rightRank {
		return leftRank < rightRank
	}
	// Higher retry count should be surfaced first within the same status band.
	if left.Retries != right.Retries {
		return left.Retries > right.Retries
	}
	// Within same status/retry bucket, show longer elapsed first.
	leftElapsed := parseElapsed(left.Elapsed)
	rightElapsed := parseElapsed(right.Elapsed)
	if leftElapsed != rightElapsed {
		return leftElapsed > rightElapsed
	}
	if left.Component != right.Component {
		return left.Component < right.Component
	}
	return left.Pipeline < right.Pipeline
}

func parseElapsed(raw string) time.Duration {
	v := strings.TrimSpace(raw)
	if v == "" || v == "-" {
		return 0
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0
	}
	return d
}

func statusSortRank(status pipestatus.Status) int {
	switch status {
	case pipestatus.StatusFailed, pipestatus.StatusExhausted, pipestatus.StatusTimeout, pipestatus.StatusQueryErr:
		return 0
	case pipestatus.StatusBackoff, pipestatus.StatusSettling, pipestatus.StatusBlocked:
		return 1
	case pipestatus.StatusPending:
		return 2
	case pipestatus.StatusRunning, pipestatus.StatusWatching:
		return 3
	case pipestatus.StatusMissing, pipestatus.StatusUnknown:
		return 4
	case pipestatus.StatusSucceeded:
		return 9
	default:
		return 5
	}
}
