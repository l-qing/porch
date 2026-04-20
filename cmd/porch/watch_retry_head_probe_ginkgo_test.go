package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"porch/pkg/config"
	"porch/pkg/gh"
	pipestatus "porch/pkg/pipeline"
	"porch/pkg/resolver"

	"github.com/sirupsen/logrus"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// checkRunPayload builds a minimal JSON check-runs response with one entry.
func checkRunPayload(status, conclusion, detailsURL string) string {
	return fmt.Sprintf(
		`{"check_runs":[{"id":1,"name":"Pipelines as Code CI / arc-all-in-one","status":%q,"conclusion":%q,"details_url":%q}]}`,
		status, conclusion, detailsURL,
	)
}

const (
	// detailsURL that encodes namespace "devops" and run "arc-all-in-one-new11".
	probeRunDetailsURL = "https://edge.alauda.cn/console-pipeline-v2/workspace/devops~business-build~devops/pipeline/pipelineRuns/detail/arc-all-in-one-new11-build-image"
)

var _ = Describe("triggerRetry head-probe (branch advanced)", func() {
	type testCase struct {
		description string
		// GH state
		headSHA         string // SHA returned by BranchSHA (new commit if != oldSHA)
		checkRunStatus  string // status field in GH check-run
		checkRunConclusion string // conclusion field
		checkRunErr     bool   // whether CheckRuns call returns an error
		noCheckRun      bool   // whether CheckRuns returns an empty list

		// Expected outcomes (dryRun=true so no real HTTP calls)
		wantRetryAdopted int // how many RETRY_ADOPTED events
		wantDryRetry     int // how many DRY_RETRY events
		wantQueryWarn    int // how many QUERY_WARN events (probe failure fallback)
		wantStatus       pipestatus.Status
		wantRetryCount   int
	}

	const (
		oldSHA = "oldsha1"
		newSHA = "newsha1"
	)

	DescribeTable("decides whether to adopt or post retry comment",
		func(tc testCase) {
			By(tc.description)

			ghc := gh.NewClient("TestGroup", fakeRunner{fn: func(args ...string) ([]byte, []byte, error) {
				joined := strings.Join(args, " ")
				switch {
				case joined == "api repos/TestGroup/arc/commits/main":
					return []byte(fmt.Sprintf(`{"sha":%q}`, tc.headSHA)), nil, nil
				case joined == fmt.Sprintf("api repos/TestGroup/arc/commits/%s/check-runs", tc.headSHA):
					if tc.checkRunErr {
						return nil, []byte("probe error"), errors.New("probe error")
					}
					if tc.noCheckRun {
						return []byte(`{"check_runs":[]}`), nil, nil
					}
					return []byte(checkRunPayload(tc.checkRunStatus, tc.checkRunConclusion, probeRunDetailsURL)), nil, nil
				default:
					return nil, []byte("unexpected: " + joined), errors.New("unexpected: " + joined)
				}
			}})

			cfg := config.RuntimeConfig{
				Root: config.Root{
					Connection: config.Connection{GitHubOrg: "TestGroup"},
					Retry: config.Retry{
						RetrySettleDelay: config.Duration{Duration: time.Second},
					},
				},
			}
			dag, err := resolver.New([]config.LoadedComponent{{Name: "arc"}}, map[string]config.Depends{})
			Expect(err).NotTo(HaveOccurred())

			retryAfter := time.Now().Add(-time.Second) // already expired
			tracked := map[string]*trackedComponent{
				"arc": {
					Name:   "arc",
					Repo:   "arc",
					Branch: "main",
					SHA:    oldSHA,
					Pipelines: map[string]*trackedPipeline{
						"arc-all-in-one": {
							Name:       "arc-all-in-one",
							Status:     pipestatus.StatusBackoff,
							RetryCmd:   "/test arc-all-in-one branch:{branch}",
							RetryAfter: &retryAfter,
							RetryCount: 0,
						},
					},
				},
			}

			events := map[watchEventKind]int{}
			emit := func(kind watchEventKind, _ string, _ logrus.Fields) { events[kind]++ }

			err = watchOnce(context.Background(), tracked, watchOnceDeps{
				log:    logrus.New(),
				cfg:    cfg,
				ghc:    ghc,
				dag:    dag,
				mode:   probeModeGHOnly,
				dryRun: true,
				emit:   emit,
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(events[eventRetryAdopted]).To(Equal(tc.wantRetryAdopted), "RETRY_ADOPTED count")
			Expect(events[eventDryRetry]).To(Equal(tc.wantDryRetry), "DRY_RETRY count")
			Expect(events[eventQueryWarn]).To(Equal(tc.wantQueryWarn), "QUERY_WARN count")

			p := tracked["arc"].Pipelines["arc-all-in-one"]
			Expect(p.Status).To(Equal(tc.wantStatus), "pipeline status")
			Expect(p.RetryCount).To(Equal(tc.wantRetryCount), "retry count")
		},

		Entry("branch advanced + new run is running → adopt, no comment", testCase{
			description:        "should emit RETRY_ADOPTED and move to Settling without posting DRY_RETRY",
			headSHA:            newSHA,
			checkRunStatus:     "in_progress",
			checkRunConclusion: "",
			wantRetryAdopted:   1,
			wantDryRetry:       0,
			wantQueryWarn:      0,
			wantStatus:         pipestatus.StatusBackoff, // dryRun leaves state unchanged
			wantRetryCount:     0,
		}),

		Entry("branch advanced + new run already succeeded → adopt, no comment", testCase{
			description:        "should emit RETRY_ADOPTED when new commit pipeline succeeded",
			headSHA:            newSHA,
			checkRunStatus:     "completed",
			checkRunConclusion: "success",
			wantRetryAdopted:   1,
			wantDryRetry:       0,
			wantQueryWarn:      0,
			wantStatus:         pipestatus.StatusBackoff,
			wantRetryCount:     0,
		}),

		Entry("branch advanced + new run failed → fall through to comment retry", testCase{
			description:        "should post DRY_RETRY when new commit pipeline already failed",
			headSHA:            newSHA,
			checkRunStatus:     "completed",
			checkRunConclusion: "failure",
			wantRetryAdopted:   0,
			wantDryRetry:       1,
			wantQueryWarn:      0,
			wantStatus:         pipestatus.StatusSettling,
			wantRetryCount:     0,
		}),

		Entry("branch advanced + no check-run yet → fall through to comment retry", testCase{
			description:   "should post DRY_RETRY when new commit has no pipeline check-run yet",
			headSHA:       newSHA,
			noCheckRun:    true,
			wantRetryAdopted: 0,
			wantDryRetry:  1,
			wantQueryWarn: 0,
			wantStatus:    pipestatus.StatusSettling,
			wantRetryCount: 0,
		}),

		Entry("branch advanced + probe error → fall through to comment retry with warning", testCase{
			description:     "should fall back to DRY_RETRY and emit QUERY_WARN when probe fails",
			headSHA:         newSHA,
			checkRunErr:     true,
			wantRetryAdopted: 0,
			wantDryRetry:    1,
			wantQueryWarn:   1,
			wantStatus:      pipestatus.StatusSettling,
			wantRetryCount:  0,
		}),

		Entry("SHA unchanged → fall through to comment retry", testCase{
			description:      "should post DRY_RETRY when HEAD commit did not advance",
			headSHA:          oldSHA, // same as initial SHA
			wantRetryAdopted: 0,
			wantDryRetry:     1,
			wantQueryWarn:    0,
			wantStatus:       pipestatus.StatusSettling,
			wantRetryCount:   0,
		}),
	)
})
