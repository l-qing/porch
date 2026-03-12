package main

import (
	"porch/pkg/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("watch scope pipeline synthesis", func() {
	type testCase struct {
		description    string
		cfg            config.RuntimeConfig
		opts           watchOptions
		wantComponents []string
		wantBranches   []string
		wantPipeline   string
		wantRetryCmd   string
	}

	DescribeTable("applyWatchScope",
		func(tc testCase) {
			scoped, err := applyWatchScope(&tc.cfg, tc.opts)
			Expect(err).NotTo(HaveOccurred())
			Expect(scoped).To(BeTrue())
			Expect(tc.cfg.Components).To(HaveLen(len(tc.wantComponents)))
			for i := range tc.cfg.Components {
				component := tc.cfg.Components[i]
				Expect(component.Name).To(Equal(tc.wantComponents[i]))
				Expect(component.Branch).To(Equal(tc.wantBranches[i]))
				Expect(component.Pipelines).To(HaveLen(1))
				Expect(component.Pipelines[0].Name).To(Equal(tc.wantPipeline))
				Expect(component.Pipelines[0].RetryCommand).To(Equal(tc.wantRetryCmd))
			}
		},
		Entry("synthesizes pipeline for existing component when pipeline is absent", testCase{
			description: "single-branch component",
			cfg: config.RuntimeConfig{
				Components: []config.LoadedComponent{
					{Name: "catalog", Repo: "catalog", Branch: "main", Pipelines: []config.PipelineSpec{{Name: "catalog-all-in-one"}}},
				},
			},
			opts: watchOptions{
				componentName: "catalog",
				pipelineName:  "catalog-special",
			},
			wantComponents: []string{"catalog"},
			wantBranches:   []string{"main"},
			wantPipeline:   "catalog-special",
			wantRetryCmd:   "/test catalog-special branch:{branch}",
		}),
		Entry("keeps branch filter and synthesizes pipeline for matched runtime component", testCase{
			description: "multi-branch component",
			cfg: config.RuntimeConfig{
				Components: []config.LoadedComponent{
					{Name: "catalog@main", Repo: "catalog", Branch: "main", Pipelines: []config.PipelineSpec{{Name: "catalog-all-in-one"}}},
					{Name: "catalog@release-1.0", Repo: "catalog", Branch: "release-1.0", Pipelines: []config.PipelineSpec{{Name: "catalog-all-in-one"}}},
				},
			},
			opts: watchOptions{
				componentName: "catalog",
				pipelineName:  "catalog-special",
				branch:        "release-1.0",
			},
			wantComponents: []string{"catalog@release-1.0"},
			wantBranches:   []string{"release-1.0"},
			wantPipeline:   "catalog-special",
			wantRetryCmd:   "/test catalog-special branch:{branch}",
		}),
	)
})
