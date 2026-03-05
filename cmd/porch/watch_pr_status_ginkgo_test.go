package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

var _ = Describe("watchOnce PR status preference", func() {
	type testCase struct {
		description         string
		kubectlPayload      string
		checkRunsPayload    string
		expectedGHCalls     int
		expectedStatus      pipestatus.Status
		expectedFailedEvent int
		expectedRetryEvent  int
		expectedGHFallback  int
	}

	DescribeTable("prefers PR check-run terminal state over kubectl running state",
		func(tc testCase) {
			By(tc.description)

			tmp := GinkgoT().TempDir()
			kubectlPath := filepath.Join(tmp, "kubectl")
			script := fmt.Sprintf("#!/bin/sh\ncat <<'EOF'\n%s\nEOF\n", tc.kubectlPayload)
			Expect(os.WriteFile(kubectlPath, []byte(script), 0o755)).To(Succeed())

			oldPath := os.Getenv("PATH")
			Expect(os.Setenv("PATH", tmp+string(os.PathListSeparator)+oldPath)).To(Succeed())
			DeferCleanup(func() {
				Expect(os.Setenv("PATH", oldPath)).To(Succeed())
			})

			ghCalls := 0
			ghc := gh.NewClient("TestGroup", fakeRunner{fn: func(args ...string) ([]byte, []byte, error) {
				ghCalls++
				joined := strings.Join(args, " ")
				if joined == "api repos/TestGroup/catalog/commits/abc123/check-runs" {
					return []byte(tc.checkRunsPayload), nil, nil
				}
				if joined == "api repos/TestGroup/catalog/pulls/686" {
					return []byte(`{"number":686,"state":"open","head":{"ref":"feat/add-golang-task","sha":"abc123"}}`), nil, nil
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
			dag, err := resolver.New([]config.LoadedComponent{{Name: "catalog#686"}}, map[string]config.Depends{})
			Expect(err).NotTo(HaveOccurred())
			tracked := map[string]*trackedComponent{
				"catalog#686": {
					Name:     "catalog#686",
					Repo:     "catalog",
					Branch:   "feat/add-golang-task",
					SHA:      "abc123",
					PRNumber: 686,
					Pipelines: map[string]*trackedPipeline{
						"catalog-all-in-one": {
							Name:        "catalog-all-in-one",
							Namespace:   "devops",
							PipelineRun: "catalog-all-in-one-wzmt7",
							Status:      pipestatus.StatusWatching,
							RetryCmd:    "/test catalog-all-in-one branch:{branch}",
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
				mode:   probeModeKubectlFirst,
				dryRun: true,
				emit:   emit,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(ghCalls).To(Equal(tc.expectedGHCalls))
			Expect(events[eventGHFallback]).To(Equal(tc.expectedGHFallback))
			Expect(events[eventFailed]).To(Equal(tc.expectedFailedEvent))
			Expect(events[eventRetrying]).To(Equal(tc.expectedRetryEvent))
			Expect(tracked["catalog#686"].Pipelines["catalog-all-in-one"].Status).To(Equal(tc.expectedStatus))
		},
		Entry("should switch to backoff when GH check-run is already failed", testCase{
			description:         "kubectl still reports running but PR check-run is failed",
			kubectlPayload:      `{"status":{"conditions":[{"type":"Succeeded","status":"Unknown","reason":"Running"}]}}`,
			checkRunsPayload:    `{"check_runs":[{"id":220,"name":"Pipelines as Code CI / catalog-all-in-one","status":"completed","conclusion":"failure","details_url":"https://x/workspace/devops~business-build~devops/pipeline/pipelineRuns/detail/catalog-all-in-one-wzmt7-build-msmtp-image"}]}`,
			expectedGHCalls:     2,
			expectedStatus:      pipestatus.StatusSettling,
			expectedFailedEvent: 1,
			expectedRetryEvent:  1,
			expectedGHFallback:  1,
		}),
		Entry("should keep running when GH check-run is still in progress", testCase{
			description:         "kubectl running and PR check-run running should stay running",
			kubectlPayload:      `{"status":{"conditions":[{"type":"Succeeded","status":"Unknown","reason":"Running"}]}}`,
			checkRunsPayload:    `{"check_runs":[{"id":221,"name":"Pipelines as Code CI / catalog-all-in-one","status":"in_progress","conclusion":"","details_url":"https://x/workspace/devops~business-build~devops/pipeline/pipelineRuns/detail/catalog-all-in-one-wzmt7-build-msmtp-image"}]}`,
			expectedGHCalls:     1,
			expectedStatus:      pipestatus.StatusRunning,
			expectedFailedEvent: 0,
			expectedRetryEvent:  0,
			expectedGHFallback:  0,
		}),
		Entry("should keep running when GH reports success but kubectl still running", testCase{
			description:         "guard against stale GH success overriding active kubectl run",
			kubectlPayload:      `{"status":{"conditions":[{"type":"Succeeded","status":"Unknown","reason":"Running"}]}}`,
			checkRunsPayload:    `{"check_runs":[{"id":222,"name":"Pipelines as Code CI / catalog-all-in-one","status":"completed","conclusion":"success","details_url":"https://x/workspace/devops~business-build~devops/pipeline/pipelineRuns/detail/catalog-all-in-one-wzmt7-build-msmtp-image"}]}`,
			expectedGHCalls:     1,
			expectedStatus:      pipestatus.StatusRunning,
			expectedFailedEvent: 0,
			expectedRetryEvent:  0,
			expectedGHFallback:  0,
		}),
	)
})
