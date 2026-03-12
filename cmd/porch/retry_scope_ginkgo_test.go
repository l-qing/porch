package main

import (
	"porch/pkg/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("retry scope pipeline synthesis", func() {
	type testCase struct {
		description   string
		components    []config.LoadedComponent
		component     string
		branch        string
		pipeline      string
		wantName      string
		wantBranch    string
		wantPipeline  string
		wantRetryCmd  string
		wantErrSubstr string
	}

	DescribeTable("resolveRetryTarget",
		func(tc testCase) {
			target, err := resolveRetryTarget(tc.components, tc.component, tc.branch, tc.pipeline)
			if tc.wantErrSubstr != "" {
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(tc.wantErrSubstr))
				return
			}

			Expect(err).NotTo(HaveOccurred())
			Expect(target.Name).To(Equal(tc.wantName))
			Expect(target.Branch).To(Equal(tc.wantBranch))
			Expect(target.Pipelines).To(HaveLen(1))
			Expect(target.Pipelines[0].Name).To(Equal(tc.wantPipeline))
			Expect(target.Pipelines[0].RetryCommand).To(Equal(tc.wantRetryCmd))
		},
		Entry("synthesizes pipeline for configured branch target", testCase{
			description: "configured branch target",
			components: []config.LoadedComponent{
				{Name: "catalog@main", Repo: "catalog", Branch: "main", Pipelines: []config.PipelineSpec{{Name: "catalog-all-in-one"}}},
			},
			component:    "catalog",
			branch:       "main",
			pipeline:     "catalog-special",
			wantName:     "catalog@main",
			wantBranch:   "main",
			wantPipeline: "catalog-special",
			wantRetryCmd: "/test catalog-special branch:{branch}",
		}),
		Entry("synthesizes pipeline for runtime branch override", testCase{
			description: "override branch on single configured component",
			components: []config.LoadedComponent{
				{Name: "catalog", Repo: "catalog", Branch: "main", Pipelines: []config.PipelineSpec{{Name: "catalog-all-in-one"}}},
			},
			component:    "catalog",
			branch:       "release-1.0",
			pipeline:     "catalog-special",
			wantName:     "catalog",
			wantBranch:   "release-1.0",
			wantPipeline: "catalog-special",
			wantRetryCmd: "/test catalog-special branch:{branch}",
		}),
	)
})
