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
			description: "watching status is shown before non-running rows",
			rows: []Row{
				{Component: "comp-fail", Branch: "main", Pipeline: "p", Status: pipestatus.StatusFailed},
				{Component: "comp-watch", Branch: "main", Pipeline: "p", Status: pipestatus.StatusWatching},
			},
			firstDataContains: "| comp-watch |",
		}),
	)
})
