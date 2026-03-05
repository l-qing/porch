package main

import (
	"context"
	"errors"
	"strings"

	"porch/pkg/config"
	"porch/pkg/gh"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("PR mode helpers", func() {
	type parseCase struct {
		description   string
		raw           string
		want          []int
		wantErrSubstr string
	}

	DescribeTable("parsePRNumbers",
		func(tc parseCase) {
			got, err := parsePRNumbers(tc.raw)
			if tc.wantErrSubstr != "" {
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(tc.wantErrSubstr))
				return
			}
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(tc.want))
		},
		Entry("returns nil for empty input", parseCase{
			description: "empty string",
			raw:         "",
			want:        nil,
		}),
		Entry("parses numbers and trims whitespace", parseCase{
			description: "normal csv",
			raw:         "123, 456 ,789",
			want:        []int{123, 456, 789},
		}),
		Entry("deduplicates repeated numbers", parseCase{
			description: "dedupe keep order",
			raw:         "123,456,123,789",
			want:        []int{123, 456, 789},
		}),
		Entry("rejects non-positive values", parseCase{
			description:   "invalid number",
			raw:           "0,123",
			wantErrSubstr: "not a positive integer",
		}),
		Entry("rejects empty csv segments", parseCase{
			description:   "empty segment",
			raw:           "123,,456",
			wantErrSubstr: "empty pr number",
		}),
	)

	type scopeCase struct {
		description   string
		cfg           config.RuntimeConfig
		component     string
		pipeline      string
		prs           []int
		expectCalls   []string
		wantNames     []string
		wantBranches  []string
		wantPRs       []int
		wantErrSubstr string
	}

	DescribeTable("applyWatchPRScope",
		func(tc scopeCase) {
			calls := make([]string, 0, 4)
			index := 0
			client := gh.NewClient("TestGroup", fakeRunner{fn: func(args ...string) ([]byte, []byte, error) {
				joined := strings.Join(args, " ")
				calls = append(calls, joined)
				if index >= len(tc.expectCalls) {
					return nil, []byte("unexpected call"), errors.New("unexpected")
				}
				if joined != tc.expectCalls[index] {
					return nil, []byte("unexpected call"), errors.New("unexpected")
				}
				index++
				switch joined {
				case "api repos/TestGroup/catalog/pulls/101":
					return []byte(`{"number":101,"state":"open","head":{"ref":"feat/a","sha":"111"}}`), nil, nil
				case "api repos/TestGroup/catalog/pulls/202":
					return []byte(`{"number":202,"state":"open","head":{"ref":"feat/b","sha":"222"}}`), nil, nil
				default:
					return nil, []byte("unexpected call"), errors.New("unexpected")
				}
			}})

			err := applyWatchPRScope(context.Background(), &tc.cfg, tc.component, tc.pipeline, tc.prs, client)
			if tc.wantErrSubstr != "" {
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(tc.wantErrSubstr))
				return
			}
			Expect(err).NotTo(HaveOccurred())
			Expect(calls).To(Equal(tc.expectCalls))
			Expect(tc.cfg.Components).To(HaveLen(len(tc.wantNames)))
			for i := range tc.cfg.Components {
				Expect(tc.cfg.Components[i].Name).To(Equal(tc.wantNames[i]))
				Expect(tc.cfg.Components[i].Branch).To(Equal(tc.wantBranches[i]))
				Expect(tc.cfg.Components[i].PRNumber).To(Equal(tc.wantPRs[i]))
			}
		},
		Entry("expands configured component by PR list", scopeCase{
			description: "configured component",
			cfg: config.RuntimeConfig{
				Root: config.Root{
					Connection: config.Connection{GitHubOrg: "TestGroup"},
				},
				Components: []config.LoadedComponent{
					{
						Name: "catalog", Repo: "catalog", BranchPatterns: []string{"^main$"},
						Pipelines: []config.PipelineSpec{{Name: "catalog-all-in-one", RetryCommand: "/test catalog-all-in-one branch:{branch}"}},
					},
				},
			},
			component:    "catalog",
			pipeline:     "catalog-all-in-one",
			prs:          []int{101, 202},
			expectCalls:  []string{"api repos/TestGroup/catalog/pulls/101", "api repos/TestGroup/catalog/pulls/202"},
			wantNames:    []string{"catalog#101", "catalog#202"},
			wantBranches: []string{"feat/a", "feat/b"},
			wantPRs:      []int{101, 202},
		}),
		Entry("supports ad-hoc component repo with pipeline", scopeCase{
			description: "ad-hoc mode",
			cfg: config.RuntimeConfig{
				Root:       config.Root{Connection: config.Connection{GitHubOrg: "TestGroup"}},
				Components: []config.LoadedComponent{},
			},
			component:    "catalog",
			pipeline:     "catalog-all-in-one",
			prs:          []int{101},
			expectCalls:  []string{"api repos/TestGroup/catalog/pulls/101"},
			wantNames:    []string{"catalog#101"},
			wantBranches: []string{"feat/a"},
			wantPRs:      []int{101},
		}),
		Entry("fails when component is missing without pipeline", scopeCase{
			description: "component required in config mode",
			cfg: config.RuntimeConfig{
				Root:       config.Root{Connection: config.Connection{GitHubOrg: "TestGroup"}},
				Components: []config.LoadedComponent{{Name: "catalog", Repo: "catalog"}},
			},
			component:     "unknown",
			prs:           []int{101},
			wantErrSubstr: "component \"unknown\" not found",
		}),
	)
})
