package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"porch/pkg/config"
	"porch/pkg/gh"
	pipestatus "porch/pkg/pipeline"
	"porch/pkg/resolver"

	"github.com/sirupsen/logrus"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("watchOnce branch head refresh", func() {
	type testCase struct {
		description        string
		headSHA            string
		checkRunsPayload   string
		initialRun         string
		initialRetryCount  int
		expectedSHA        string
		expectedRun        string
		expectedStatus     pipestatus.Status
		expectedRetryCount int
		expectedHeadEvents int
		expectedGHCalls    int
	}

	DescribeTable("switches tracking to latest head commit when component is already succeeded",
		func(tc testCase) {
			By(tc.description)

			ghCalls := 0
			ghc := gh.NewClient("TestGroup", fakeRunner{fn: func(args ...string) ([]byte, []byte, error) {
				ghCalls++
				joined := strings.Join(args, " ")
				switch joined {
				case "api repos/TestGroup/catalog/commits/main":
					return []byte(fmt.Sprintf(`{"sha":"%s"}`, tc.headSHA)), nil, nil
				case fmt.Sprintf("api repos/TestGroup/catalog/commits/%s/check-runs", tc.headSHA):
					return []byte(tc.checkRunsPayload), nil, nil
				default:
					return nil, []byte("unexpected args"), errors.New("unexpected")
				}
			}})

			cfg := config.RuntimeConfig{
				Root: config.Root{
					Connection: config.Connection{
						GitHubOrg: "TestGroup",
					},
				},
			}
			dag, err := resolver.New([]config.LoadedComponent{{Name: "catalog"}}, map[string]config.Depends{})
			Expect(err).NotTo(HaveOccurred())

			tracked := map[string]*trackedComponent{
				"catalog": {
					Name:   "catalog",
					Repo:   "catalog",
					Branch: "main",
					SHA:    "abc123",
					Pipelines: map[string]*trackedPipeline{
						"catalog-all-in-one": {
							Name:        "catalog-all-in-one",
							Status:      pipestatus.StatusSucceeded,
							Conclusion:  pipestatus.ConclusionSuccess,
							Namespace:   "devops",
							PipelineRun: tc.initialRun,
							RetryCount:  tc.initialRetryCount,
						},
					},
				},
			}

			events := map[watchEventKind]int{}
			emit := func(kind watchEventKind, _ string, _ logrus.Fields) {
				events[kind]++
			}

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

			Expect(ghCalls).To(Equal(tc.expectedGHCalls))
			Expect(events[eventHeadUpdate]).To(Equal(tc.expectedHeadEvents))
			Expect(tracked["catalog"].SHA).To(Equal(tc.expectedSHA))

			p := tracked["catalog"].Pipelines["catalog-all-in-one"]
			Expect(p.Status).To(Equal(tc.expectedStatus))
			Expect(p.PipelineRun).To(Equal(tc.expectedRun))
			Expect(p.RetryCount).To(Equal(tc.expectedRetryCount))
		},
		Entry("should switch to new head and follow the new running pipeline run", testCase{
			description:        "head commit moved from abc123 to def456",
			headSHA:            "def456",
			checkRunsPayload:   `{"check_runs":[{"id":301,"name":"Pipelines as Code CI / catalog-all-in-one","status":"in_progress","conclusion":"","details_url":"https://x/workspace/devops~business-build~devops/pipeline/pipelineRuns/detail/catalog-all-in-one-n3w45-build-catalog-image"}]}`,
			initialRun:         "catalog-all-in-one-old11",
			initialRetryCount:  2,
			expectedSHA:        "def456",
			expectedRun:        "catalog-all-in-one-n3w45",
			expectedStatus:     pipestatus.StatusRunning,
			expectedRetryCount: 0,
			expectedHeadEvents: 1,
			expectedGHCalls:    3,
		}),
		Entry("should keep existing run when head commit is unchanged", testCase{
			description:        "head commit remains abc123",
			headSHA:            "abc123",
			checkRunsPayload:   `{"check_runs":[{"id":302,"name":"Pipelines as Code CI / catalog-all-in-one","status":"completed","conclusion":"success","details_url":"https://x/workspace/devops~business-build~devops/pipeline/pipelineRuns/detail/catalog-all-in-one-old11-build-catalog-image"}]}`,
			initialRun:         "catalog-all-in-one-old11",
			initialRetryCount:  2,
			expectedSHA:        "abc123",
			expectedRun:        "catalog-all-in-one-old11",
			expectedStatus:     pipestatus.StatusSucceeded,
			expectedRetryCount: 2,
			expectedHeadEvents: 0,
			expectedGHCalls:    2,
		}),
	)
})
