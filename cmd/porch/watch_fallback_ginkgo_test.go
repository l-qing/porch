package main

import (
	"context"
	"errors"
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

var _ = Describe("watchOnce GH fallback for missing runtime run", func() {
	type testCase struct {
		description      string
		checkRunsPayload string
		expectedStatus   pipestatus.Status
		expectRetry      bool
		expectedGHCalls  int
	}

	DescribeTable("uses GH check-runs when pipeline run name is missing",
		func(tc testCase) {
			By(tc.description)
			ghCalls := 0
			ghc := gh.NewClient("TestGroup", fakeRunner{fn: func(args ...string) ([]byte, []byte, error) {
				ghCalls++
				joined := strings.Join(args, " ")
				if joined == "api repos/TestGroup/tektoncd-operator/commits/abc123/check-runs" {
					return []byte(tc.checkRunsPayload), nil, nil
				}
				if joined == "api repos/TestGroup/tektoncd-operator/commits/release-4.6" {
					return []byte(`{"sha":"abc123"}`), nil, nil
				}
				return nil, []byte("unexpected args"), errors.New("unexpected")
			}})

			cfg := config.RuntimeConfig{
				Root: config.Root{
					Connection: config.Connection{
						GitHubOrg: "TestGroup",
					},
					Retry: config.Retry{
						MaxRetries: 0,
						Backoff: config.Backoff{
							Initial:    config.Duration{Duration: time.Second},
							Multiplier: 1,
							Max:        config.Duration{Duration: time.Second},
						},
					},
				},
			}
			dag, err := resolver.New([]config.LoadedComponent{{Name: "tektoncd-operator@release-4.6"}}, map[string]config.Depends{})
			Expect(err).NotTo(HaveOccurred())
			tracked := map[string]*trackedComponent{
				"tektoncd-operator@release-4.6": {
					Name:   "tektoncd-operator@release-4.6",
					Repo:   "tektoncd-operator",
					Branch: "release-4.6",
					SHA:    "abc123",
					Pipelines: map[string]*trackedPipeline{
						"to-all-in-one": {
							Name:       "to-all-in-one",
							Namespace:  "devops",
							Status:     pipestatus.StatusWatching,
							RetryCmd:   "/test to-all-in-one branch:{branch}",
							RetryCount: 0,
						},
					},
				},
			}
			events := map[watchEventKind]int{}
			emit := func(kind watchEventKind, _ string, _ logrus.Fields) {
				events[kind]++
			}
			deps := watchOnceDeps{
				log:    logrus.New(),
				cfg:    cfg,
				ghc:    ghc,
				dag:    dag,
				mode:   probeModeKubectlFirst,
				dryRun: true,
				emit:   emit,
			}

			err = watchOnce(context.Background(), tracked, deps)
			Expect(err).NotTo(HaveOccurred())
			Expect(ghCalls).To(Equal(tc.expectedGHCalls))
			Expect(events[eventGHFallback]).To(Equal(1))

			p := tracked["tektoncd-operator@release-4.6"].Pipelines["to-all-in-one"]
			Expect(p.Status).To(Equal(tc.expectedStatus))
			if tc.expectRetry {
				Expect(events[eventFailed]).To(Equal(1))
				Expect(events[eventRetrying]).To(Equal(1))
				Expect(events[eventDryRetry]).To(Equal(1))
				Expect(p.RetryAfter).To(BeNil())
				Expect(p.SettleAfter).NotTo(BeNil())
			} else {
				Expect(events[eventSuccess]).To(Equal(1))
				Expect(p.RetryAfter).To(BeNil())
			}
		},
		Entry("failed check-run should enter backoff retry", testCase{
			description:      "should mark pipeline failed and schedule retry when GH reports failure",
			checkRunsPayload: `{"check_runs":[{"id":12,"name":"Pipelines as Code CI / to-all-in-one","status":"completed","conclusion":"failure","details_url":"https://x/workspace/devops~business-build~devops/pipeline/pipelineRuns/detail/to-all-in-one-abc12"}]}`,
			expectedStatus:   pipestatus.StatusSettling,
			expectRetry:      true,
			expectedGHCalls:  2,
		}),
		Entry("successful check-run should become succeeded", testCase{
			description:      "should mark pipeline succeeded when GH reports success",
			checkRunsPayload: `{"check_runs":[{"id":13,"name":"Pipelines as Code CI / to-all-in-one","status":"completed","conclusion":"success","details_url":"https://x/workspace/devops~business-build~devops/pipeline/pipelineRuns/detail/to-all-in-one-xyz99"}]}`,
			expectedStatus:   pipestatus.StatusSucceeded,
			expectRetry:      false,
			expectedGHCalls:  2,
		}),
	)
})
