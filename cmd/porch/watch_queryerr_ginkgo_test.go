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

var _ = Describe("watchOnce query error escalation", func() {
	type testCase struct {
		description      string
		maxRetries       int
		retryCount       int
		expectedStatus   pipestatus.Status
		expectRetrying   bool
		expectExhausted  bool
		expectedGHCalls  int
		expectedFailed   bool
		expectedQueryErr bool
	}

	DescribeTable("escalates query threshold failures",
		func(tc testCase) {
			By(tc.description)

			ghCalls := 0
			ghc := gh.NewClient("TestGroup", fakeRunner{fn: func(args ...string) ([]byte, []byte, error) {
				ghCalls++
				joined := strings.Join(args, " ")
				switch joined {
				case "api repos/TestGroup/tektoncd-operator/commits/abc123/check-runs":
					// Deliberately return unmatched check-runs so fallback lookup fails.
					return []byte(`{"check_runs":[{"name":"Pipelines as Code CI / other-pipeline","status":"completed","conclusion":"success"}]}`), nil, nil
				case "api repos/TestGroup/tektoncd-operator/commits/release-4.7":
					return []byte(`{"sha":"abc123"}`), nil, nil
				default:
					return nil, []byte("unexpected args"), errors.New("unexpected")
				}
			}})

			cfg := config.RuntimeConfig{
				Root: config.Root{
					Connection: config.Connection{
						GitHubOrg: "TestGroup",
					},
					Retry: config.Retry{
						MaxRetries: tc.maxRetries,
						Backoff: config.Backoff{
							Initial:    config.Duration{Duration: time.Second},
							Multiplier: 1,
							Max:        config.Duration{Duration: time.Second},
						},
					},
				},
			}
			dag, err := resolver.New([]config.LoadedComponent{{Name: "tektoncd-operator@release-4.7"}}, map[string]config.Depends{})
			Expect(err).NotTo(HaveOccurred())
			tracked := map[string]*trackedComponent{
				"tektoncd-operator@release-4.7": {
					Name:   "tektoncd-operator@release-4.7",
					Repo:   "tektoncd-operator",
					Branch: "release-4.7",
					SHA:    "abc123",
					Pipelines: map[string]*trackedPipeline{
						"to-all-in-one": {
							Name:        "to-all-in-one",
							Status:      pipestatus.StatusWatching,
							RetryCmd:    "/test to-all-in-one branch:{branch}",
							RetryCount:  tc.retryCount,
							QueryErrors: 4,
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
				mode:   probeModeGHOnly,
				dryRun: true,
				emit:   emit,
			}

			err = watchOnce(context.Background(), tracked, deps)
			Expect(err).NotTo(HaveOccurred())
			Expect(ghCalls).To(Equal(tc.expectedGHCalls))

			p := tracked["tektoncd-operator@release-4.7"].Pipelines["to-all-in-one"]
			Expect(p.Status).To(Equal(tc.expectedStatus))
			Expect(events[eventRetrying] > 0).To(Equal(tc.expectRetrying))
			Expect(events[eventExhausted] > 0).To(Equal(tc.expectExhausted))
			Expect(events[eventFailed] > 0).To(Equal(tc.expectedFailed))
			Expect(events[eventQueryErr] > 0).To(Equal(tc.expectedQueryErr))
		},
		Entry("query error threshold should transition to backoff retry", testCase{
			description:      "retry is still available",
			maxRetries:       0,
			retryCount:       0,
			expectedStatus:   pipestatus.StatusSettling,
			expectRetrying:   true,
			expectExhausted:  false,
			expectedGHCalls:  4,
			expectedFailed:   true,
			expectedQueryErr: true,
		}),
		Entry("query error threshold should transition to exhausted when retries are used up", testCase{
			description:      "retry limit reached",
			maxRetries:       1,
			retryCount:       1,
			expectedStatus:   pipestatus.StatusExhausted,
			expectRetrying:   false,
			expectExhausted:  true,
			expectedGHCalls:  3,
			expectedFailed:   false,
			expectedQueryErr: true,
		}),
	)
})
