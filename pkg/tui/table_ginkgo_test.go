package tui

import (
	"strings"

	pipestatus "porch/pkg/pipeline"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("MarkdownTable", func() {
	type testCase struct {
		description       string
		rows              []Row
		firstDataContains string
	}

	firstDataLine := func(markdown string) string {
		lines := strings.Split(markdown, "\n")
		dataCount := 0
		for _, line := range lines {
			if !strings.HasPrefix(line, "|") {
				continue
			}
			dataCount++
			// 1: header, 2: align, 3+: data rows
			if dataCount == 3 {
				return line
			}
		}
		return ""
	}

	DescribeTable("orders rows in notifications",
		func(tc testCase) {
			By(tc.description)
			got := MarkdownTable(tc.rows)
			Expect(firstDataLine(got)).To(ContainSubstring(tc.firstDataContains))
		},
		Entry("running row should be listed before succeeded row", testCase{
			description: "prioritize in-progress status",
			rows: []Row{
				{Component: "comp-ok", Branch: "main", Pipeline: "p", Status: pipestatus.StatusSucceeded},
				{Component: "comp-run", Branch: "main", Pipeline: "p", Status: pipestatus.StatusRunning},
			},
			firstDataContains: "| comp-run |",
		}),
		Entry("watching row should also be treated as in-progress", testCase{
			description: "failure rows are prioritized before running rows",
			rows: []Row{
				{Component: "comp-fail", Branch: "main", Pipeline: "p", Status: pipestatus.StatusFailed},
				{Component: "comp-watch", Branch: "main", Pipeline: "p", Status: pipestatus.StatusWatching},
			},
			firstDataContains: "| comp-fail |",
		}),
	)

	type runURLCase struct {
		description    string
		rows           []Row
		wantSubstrings []string
	}

	DescribeTable("renders pipeline run URLs",
		func(tc runURLCase) {
			By(tc.description)
			got := MarkdownTable(tc.rows)
			for _, want := range tc.wantSubstrings {
				Expect(got).To(ContainSubstring(want))
			}
		},
		Entry("adds pipeline hyperlink without extra columns", runURLCase{
			description: "pipeline column is clickable",
			rows: []Row{
				{
					Component: "comp",
					Branch:    "main",
					Pipeline:  "p",
					Status:    pipestatus.StatusRunning,
					Retries:   1,
					Run:       "tt-all-in-one-qgsbn",
					RunURL:    "https://edge.alauda.cn/console-pipeline-v2/workspace/devops~business-build~devops/pipeline/pipelineRuns/detail/tt-all-in-one-qgsbn",
				},
			},
			wantSubstrings: []string{
				"| Component | Branch | Pipeline | Status | Retries |",
				"[p](https://edge.alauda.cn/console-pipeline-v2/workspace/devops~business-build~devops/pipeline/pipelineRuns/detail/tt-all-in-one-qgsbn)",
			},
		}),
		Entry("keeps plain pipeline text when run URL is unavailable", runURLCase{
			description: "missing run url",
			rows: []Row{
				{
					Component: "comp",
					Branch:    "main",
					Pipeline:  "p",
					Status:    pipestatus.StatusSucceeded,
					Retries:   0,
					Run:       "tt-all-in-one-qgsbn",
				},
			},
			wantSubstrings: []string{
				"| comp | main | p | OK | 0 |",
			},
		}),
	)
})

var _ = Describe("TerminalTable", func() {
	type testCase struct {
		description    string
		rows           []Row
		wantSubstrings []string
	}

	DescribeTable("renders run URL column",
		func(tc testCase) {
			By(tc.description)
			got := TerminalTable(tc.rows)
			for _, want := range tc.wantSubstrings {
				Expect(got).To(ContainSubstring(want))
			}
		},
		Entry("shows full run URL when available", testCase{
			description: "contains RunURL header and value",
			rows: []Row{
				{
					Component: "comp",
					Branch:    "main",
					Pipeline:  "p",
					Status:    pipestatus.StatusRunning,
					Retries:   1,
					Run:       "tt-all-in-one-qgsbn",
					RunURL:    "https://edge.alauda.cn/console-pipeline-v2/workspace/devops~business-build~devops/pipeline/pipelineRuns/detail/tt-all-in-one-qgsbn",
				},
			},
			wantSubstrings: []string{
				"RunURL",
				"tt-all-in-one-qgsbn",
				"https://edge.alauda.cn/console-pipeline-v2/workspace/devops~business-build~devops/pipeline/pipelineRuns/detail/tt-all-in-one-qgsbn",
			},
		}),
		Entry("shows dash when run URL is unavailable", testCase{
			description: "missing run url",
			rows: []Row{
				{
					Component: "comp",
					Branch:    "main",
					Pipeline:  "p",
					Status:    pipestatus.StatusSucceeded,
					Retries:   0,
					Run:       "tt-all-in-one-qgsbn",
				},
			},
			wantSubstrings: []string{
				"RunURL",
				"tt-all-in-one-qgsbn",
				"-",
			},
		}),
	)

	type orderCase struct {
		description string
		rows        []Row
		left        string
		right       string
	}

	DescribeTable("prioritizes non-ok rows",
		func(tc orderCase) {
			By(tc.description)
			got := TerminalTable(tc.rows)
			leftIdx := strings.Index(got, tc.left)
			rightIdx := strings.Index(got, tc.right)
			Expect(leftIdx).To(BeNumerically(">=", 0))
			Expect(rightIdx).To(BeNumerically(">=", 0))
			Expect(leftIdx).To(BeNumerically("<", rightIdx))
		},
		Entry("failed row appears before succeeded row", orderCase{
			description: "non-ok first",
			rows: []Row{
				{Component: "comp-ok", Branch: "main", Pipeline: "p", Status: pipestatus.StatusSucceeded},
				{Component: "comp-fail", Branch: "main", Pipeline: "p", Status: pipestatus.StatusFailed},
			},
			left:  "comp-fail",
			right: "comp-ok",
		}),
		Entry("running row appears before succeeded row", orderCase{
			description: "running is still non-ok",
			rows: []Row{
				{Component: "comp-ok", Branch: "main", Pipeline: "p", Status: pipestatus.StatusSucceeded},
				{Component: "comp-run", Branch: "main", Pipeline: "p", Status: pipestatus.StatusRunning},
			},
			left:  "comp-run",
			right: "comp-ok",
		}),
	)
})
