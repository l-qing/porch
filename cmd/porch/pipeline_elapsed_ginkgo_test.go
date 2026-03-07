package main

import (
	"time"

	pipestatus "porch/pkg/pipeline"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("pipelineElapsedText", func() {
	type testCase struct {
		description      string
		startedAt        *time.Time
		completedAt      *time.Time
		lastTransitionAt *time.Time
		status           pipestatus.Status
		now              time.Time
		want             string
	}

	mustTime := func(raw string) *time.Time {
		parsed, err := time.Parse(time.RFC3339, raw)
		Expect(err).NotTo(HaveOccurred())
		return &parsed
	}

	DescribeTable("calculates elapsed with Tekton timestamps",
		func(tc testCase) {
			By(tc.description)
			got := pipelineElapsedText(tc.startedAt, tc.completedAt, tc.lastTransitionAt, tc.status, tc.now)
			Expect(got).To(Equal(tc.want))
		},
		Entry("uses now for running pipeline", testCase{
			description: "running duration",
			startedAt:   mustTime("2026-03-07T06:05:28Z"),
			status:      pipestatus.StatusRunning,
			now:         mustTime("2026-03-07T06:20:56Z").UTC(),
			want:        "15m28s",
		}),
		Entry("uses completionTime for terminal pipeline", testCase{
			description: "completed duration",
			startedAt:   mustTime("2026-03-05T13:05:28Z"),
			completedAt: mustTime("2026-03-05T13:27:44Z"),
			status:      pipestatus.StatusSucceeded,
			now:         mustTime("2026-03-05T13:30:00Z").UTC(),
			want:        "22m16s",
		}),
		Entry("falls back to lastTransitionTime when completionTime is missing", testCase{
			description:      "failed duration fallback",
			startedAt:        mustTime("2026-03-07T06:05:28Z"),
			lastTransitionAt: mustTime("2026-03-07T06:20:56Z"),
			status:           pipestatus.StatusFailed,
			now:              mustTime("2026-03-07T06:30:00Z").UTC(),
			want:             "15m28s",
		}),
		Entry("returns dash when startTime is missing", testCase{
			description: "missing start",
			status:      pipestatus.StatusRunning,
			now:         mustTime("2026-03-07T06:30:00Z").UTC(),
			want:        "-",
		}),
	)
})
