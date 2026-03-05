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

var _ = Describe("watchOnce retry comment target", func() {
	type testCase struct {
		description   string
		prNumber      int
		expectCalls   []string
		wantErrSubstr string
	}

	DescribeTable("sends retry comments to expected target",
		func(tc testCase) {
			calls := make([]string, 0, 4)
			callIndex := 0
			ghc := gh.NewClient("TestGroup", fakeRunner{fn: func(args ...string) ([]byte, []byte, error) {
				joined := strings.Join(args, " ")
				calls = append(calls, joined)
				if callIndex >= len(tc.expectCalls) {
					return nil, []byte("unexpected"), errors.New("unexpected")
				}
				if joined != tc.expectCalls[callIndex] {
					return nil, []byte("unexpected"), errors.New("unexpected")
				}
				callIndex++
				if strings.HasPrefix(joined, "api repos/TestGroup/catalog/commits/feat%2Fa") {
					return []byte(`{"sha":"abc123"}`), nil, nil
				}
				if joined == "api repos/TestGroup/catalog/pulls/101" {
					return []byte(`{"number":101,"state":"open","head":{"ref":"feat/a","sha":"abc123"}}`), nil, nil
				}
				return nil, nil, nil
			}})

			cfg := config.RuntimeConfig{
				Root: config.Root{
					Connection: config.Connection{GitHubOrg: "TestGroup"},
					Retry: config.Retry{
						RetrySettleDelay: config.Duration{Duration: time.Second},
					},
				},
			}
			dag, err := resolver.New([]config.LoadedComponent{{Name: "catalog#101"}}, map[string]config.Depends{})
			Expect(err).NotTo(HaveOccurred())
			retryAfter := time.Now().Add(-1 * time.Second)
			tracked := map[string]*trackedComponent{
				"catalog#101": {
					Name:     "catalog#101",
					Repo:     "catalog",
					Branch:   "feat/a",
					SHA:      "old",
					PRNumber: tc.prNumber,
					Pipelines: map[string]*trackedPipeline{
						"catalog-all-in-one": {
							Name:       "catalog-all-in-one",
							Status:     pipestatus.StatusBackoff,
							RetryCmd:   "/test catalog-all-in-one branch:{branch}",
							RetryAfter: &retryAfter,
						},
					},
				},
			}

			err = watchOnce(context.Background(), tracked, watchOnceDeps{
				log:  logrus.New(),
				cfg:  cfg,
				ghc:  ghc,
				dag:  dag,
				mode: probeModeGHOnly,
				emit: func(watchEventKind, string, logrus.Fields) {},
			})
			if tc.wantErrSubstr != "" {
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(tc.wantErrSubstr))
				return
			}
			Expect(err).NotTo(HaveOccurred())
			Expect(calls).To(Equal(tc.expectCalls))
		},
		Entry("posts retry command to commit comments when PR is not bound", testCase{
			description: "default mode",
			prNumber:    0,
			expectCalls: []string{
				"api repos/TestGroup/catalog/commits/feat%2Fa",
				"api repos/TestGroup/catalog/commits/abc123/comments -f body=/test catalog-all-in-one branch:feat/a",
			},
		}),
		Entry("posts retry command to pull request comments when PR is bound", testCase{
			description: "pr mode",
			prNumber:    101,
			expectCalls: []string{
				"api repos/TestGroup/catalog/pulls/101",
				"api repos/TestGroup/catalog/issues/101/comments -f body=/test catalog-all-in-one branch:feat/a",
			},
		}),
	)
})
