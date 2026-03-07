package main

import (
	"porch/pkg/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("pipelineRunDetailURL", func() {
	type testCase struct {
		description string
		namespace   string
		pipelineRun string
		connection  config.Connection
		want        string
	}

	DescribeTable("builds console URL",
		func(tc testCase) {
			By(tc.description)
			Expect(pipelineRunDetailURL(tc.namespace, tc.pipelineRun, tc.connection)).To(Equal(tc.want))
		},
		Entry("builds full detail path", testCase{
			description: "normal namespace and run",
			namespace:   "devops",
			pipelineRun: "tt-all-in-one-qgsbn",
			want:        "https://edge.alauda.cn/console-pipeline-v2/workspace/devops~business-build~devops/pipeline/pipelineRuns/detail/tt-all-in-one-qgsbn",
		}),
		Entry("returns empty when namespace is missing", testCase{
			description: "missing namespace",
			namespace:   "",
			pipelineRun: "tt-all-in-one-qgsbn",
			want:        "",
		}),
		Entry("returns empty when run is missing", testCase{
			description: "missing run",
			namespace:   "devops",
			pipelineRun: "",
			want:        "",
		}),
		Entry("supports custom console host and workspace name", testCase{
			description: "custom environment",
			namespace:   "ns-prod",
			pipelineRun: "run-001",
			connection: config.Connection{
				PipelineConsoleBaseURL: "https://edge-prod.alauda.cn/console-pipeline-v2",
				PipelineWorkspaceName:  "business-release",
			},
			want: "https://edge-prod.alauda.cn/console-pipeline-v2/workspace/ns-prod~business-release~ns-prod/pipeline/pipelineRuns/detail/run-001",
		}),
	)
})
