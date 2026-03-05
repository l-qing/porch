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
	Run       string
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
	sorted := make([]Row, len(rows))
	copy(sorted, rows)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Component == sorted[j].Component {
			return sorted[i].Pipeline < sorted[j].Pipeline
		}
		return sorted[i].Component < sorted[j].Component
	})

	headers := []string{"Component", "Branch", "Pipeline", "Status", "Retries", "Run"}
	widths := []int{len(headers[0]), len(headers[1]), len(headers[2]), len(headers[3]), len(headers[4]), len(headers[5])}

	for _, row := range sorted {
		status := renderStatus(row.Status)
		run := strings.TrimSpace(row.Run)
		if run == "" {
			run = "-"
		}
		retries := strconv.Itoa(row.Retries)

		widths[0] = max(widths[0], len(row.Component))
		widths[1] = max(widths[1], len(row.Branch))
		widths[2] = max(widths[2], len(row.Pipeline))
		widths[3] = max(widths[3], len(status))
		widths[4] = max(widths[4], len(retries))
		widths[5] = max(widths[5], len(run))
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%-*s  %-*s  %-*s  %-*s  %-*s  %-*s\n",
		widths[0], headers[0],
		widths[1], headers[1],
		widths[2], headers[2],
		widths[3], headers[3],
		widths[4], headers[4],
		widths[5], headers[5],
	))
	sb.WriteString(fmt.Sprintf("%s  %s  %s  %s  %s  %s\n",
		strings.Repeat("-", widths[0]),
		strings.Repeat("-", widths[1]),
		strings.Repeat("-", widths[2]),
		strings.Repeat("-", widths[3]),
		strings.Repeat("-", widths[4]),
		strings.Repeat("-", widths[5]),
	))
	sb.WriteString(fmt.Sprintf("Summary: succeeded=%d/%d\n\n", countSucceeded(sorted), len(sorted)))

	for _, row := range sorted {
		run := strings.TrimSpace(row.Run)
		if run == "" {
			run = "-"
		}
		sb.WriteString(fmt.Sprintf("%-*s  %-*s  %-*s  %-*s  %*d  %-*s\n",
			widths[0], row.Component,
			widths[1], row.Branch,
			widths[2], row.Pipeline,
			widths[3], renderStatus(row.Status),
			widths[4], row.Retries,
			widths[5], run,
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
	sorted := make([]Row, len(rows))
	copy(sorted, rows)
	sort.Slice(sorted, func(i, j int) bool {
		leftInProgress := isInProgressStatus(sorted[i].Status)
		rightInProgress := isInProgressStatus(sorted[j].Status)
		if leftInProgress != rightInProgress {
			return leftInProgress
		}
		if sorted[i].Component == sorted[j].Component {
			return sorted[i].Pipeline < sorted[j].Pipeline
		}
		return sorted[i].Component < sorted[j].Component
	})

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("succeeded=%d/%d\n\n", countSucceeded(sorted), len(sorted)))

	sb.WriteString("| Component | Branch | Pipeline | Status | Retries |\n")
	sb.WriteString("| :--- | :--- | :--- | :---: | :---: |\n")
	for _, r := range sorted {
		component := r.Component
		if r.CommitURL != "" {
			component = fmt.Sprintf("[%s](%s)", r.Component, r.CommitURL)
		}
		branch := r.Branch
		if r.BranchURL != "" {
			branch = fmt.Sprintf("[%s](%s)", r.Branch, r.BranchURL)
		}
		sb.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %d |\n",
			component, branch, r.Pipeline,
			renderStatus(r.Status), r.Retries))
	}
	return sb.String()
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func isInProgressStatus(status pipestatus.Status) bool {
	return status == pipestatus.StatusRunning || status == pipestatus.StatusWatching
}
