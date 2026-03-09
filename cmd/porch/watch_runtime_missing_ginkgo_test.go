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

var _ = Describe("watchOnce runtime missing fallback", func() {
	type testCase struct {
		description         string
		kubectlErr          string
		expectedStaleReason string
	}

	DescribeTable("forces retry after repeated missing runtime probes",
		func(tc testCase) {
			By(tc.description)

			tmp := GinkgoT().TempDir()
			kubectlPath := filepath.Join(tmp, "kubectl")
			script := fmt.Sprintf("#!/bin/sh\necho '%s' >&2\nexit 1\n", tc.kubectlErr)
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
				if joined == "api repos/TestGroup/tektoncd-pipeline/commits/abc123/check-runs" {
					return []byte(`{"check_runs":[{"id":301,"name":"Pipelines as Code CI / tp-all-in-one","status":"in_progress","conclusion":"","details_url":"https://x/workspace/devops~business-build~devops/pipeline/pipelineRuns/detail/tp-all-in-one-5dcpx-build-image"}]}`), nil, nil
				}
				if joined == "api repos/TestGroup/tektoncd-pipeline/commits/release-1.6" {
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
						RetrySettleDelay: config.Duration{Duration: time.Second},
					},
				},
			}
			dag, err := resolver.New([]config.LoadedComponent{{Name: "tektoncd-pipeline@release-1.6"}}, map[string]config.Depends{})
			Expect(err).NotTo(HaveOccurred())

			tracked := map[string]*trackedComponent{
				"tektoncd-pipeline@release-1.6": {
					Name:   "tektoncd-pipeline@release-1.6",
					Repo:   "tektoncd-pipeline",
					Branch: "release-1.6",
					SHA:    "abc123",
					Pipelines: map[string]*trackedPipeline{
						"tp-all-in-one": {
							Name:        "tp-all-in-one",
							Namespace:   "devops",
							PipelineRun: "tp-all-in-one-5dcpx",
							Status:      pipestatus.StatusWatching,
							RetryCmd:    "/test tp-all-in-one branch:{branch}",
						},
					},
				},
			}

			events := map[watchEventKind]int{}
			runStaleReason := ""
			emit := func(kind watchEventKind, _ string, fields logrus.Fields) {
				events[kind]++
				if kind == eventRunStale {
					if reason, ok := fields["reason"].(string); ok {
						runStaleReason = reason
					}
				}
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
			for i := 0; i < runMismatchRetryThreshold+2; i++ {
				err = watchOnce(context.Background(), tracked, deps)
				Expect(err).NotTo(HaveOccurred())
				p := tracked["tektoncd-pipeline@release-1.6"].Pipelines["tp-all-in-one"]
				if p.Status == pipestatus.StatusSettling {
					break
				}
			}

			Expect(ghCalls).To(BeNumerically(">=", runMismatchRetryThreshold))
			Expect(events[eventRunStale]).To(Equal(1))
			Expect(events[eventFailed]).To(Equal(1))
			Expect(events[eventRetrying]).To(Equal(1))
			Expect(events[eventDryRetry]).To(Equal(1))
			Expect(runStaleReason).To(Equal(tc.expectedStaleReason))

			p := tracked["tektoncd-pipeline@release-1.6"].Pipelines["tp-all-in-one"]
			Expect(p.Status).To(Equal(pipestatus.StatusSettling))
			Expect(p.SettleAfter).NotTo(BeNil())
			Expect(p.RetryAfter).To(BeNil())
		},
		Entry("handles kubectl NotFound error text", testCase{
			description:         "should trigger retry when runtime run keeps missing from kubectl",
			kubectlErr:          `Error from server (NotFound): pipelineruns.tekton.dev "tp-all-in-one-5dcpx" not found`,
			expectedStaleReason: "gh_fallback_runtime_missing_threshold",
		}),
	)

	DescribeTable("isPipelineRunNotFoundError",
		func(err error, want bool) {
			Expect(isPipelineRunNotFoundError(err)).To(Equal(want))
		},
		Entry("matches kubernetes NotFound token", errors.New(`Error from server (NotFound): pipelineruns.tekton.dev "x" not found`), true),
		Entry("matches plain not found text", errors.New("pipeline run not found"), true),
		Entry("ignores unrelated errors", errors.New("connection refused"), false),
		Entry("handles nil", nil, false),
	)
})
