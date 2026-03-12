package config

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("pipeline retry command defaults", func() {
	type normalizeCase struct {
		description string
		spec        PipelineSpec
		wantName    string
		wantRetry   string
	}

	DescribeTable("NormalizePipelineSpec",
		func(tc normalizeCase) {
			got := NormalizePipelineSpec(tc.spec)
			Expect(got.Name).To(Equal(tc.wantName))
			Expect(got.RetryCommand).To(Equal(tc.wantRetry))
		},
		Entry("keeps explicit retry command", normalizeCase{
			description: "custom retry command",
			spec: PipelineSpec{
				Name:         "catalog-all-in-one",
				RetryCommand: "/test custom branch:{branch}",
			},
			wantName:  "catalog-all-in-one",
			wantRetry: "/test custom branch:{branch}",
		}),
		Entry("synthesizes default retry command when omitted", normalizeCase{
			description: "default retry command",
			spec: PipelineSpec{
				Name: "catalog-all-in-one",
			},
			wantName:  "catalog-all-in-one",
			wantRetry: "/test catalog-all-in-one branch:{branch}",
		}),
	)

	type loadCase struct {
		description string
		pipelineYML string
		wantRetry   string
	}

	DescribeTable("Load applies pipeline retry defaults",
		func(tc loadCase) {
			dir := GinkgoT().TempDir()
			orchestrator := filepath.Join(dir, "orchestrator.yaml")
			components := filepath.Join(dir, "components.yaml")

			orchestratorYAML := `apiVersion: porch/v1
kind: ReleaseOrchestration
metadata: {name: test}
connection: {github_org: TestGroup}
watch: {interval: 30s}
retry:
  max_retries: 1
  backoff: {initial: 1m, multiplier: 1.5, max: 5m}
timeout: {global: 1h}
components_file: ./components.yaml
components:
  - name: catalog
    repo: catalog
    pipelines:
` + tc.pipelineYML + `
`

			Expect(os.WriteFile(orchestrator, []byte(orchestratorYAML), 0o644)).To(Succeed())
			Expect(os.WriteFile(components, []byte(`catalog: {revision: main}`), 0o644)).To(Succeed())

			cfg, err := Load(orchestrator)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Components).To(HaveLen(1))
			Expect(cfg.Components[0].Pipelines).To(HaveLen(1))
			Expect(cfg.Components[0].Pipelines[0].RetryCommand).To(Equal(tc.wantRetry))
		},
		Entry("uses explicit retry command from config", loadCase{
			description: "explicit retry command",
			pipelineYML: "      - name: catalog-all-in-one\n        retry_command: /test custom branch:{branch}",
			wantRetry:   "/test custom branch:{branch}",
		}),
		Entry("fills default retry command when config omits it", loadCase{
			description: "retry command omitted",
			pipelineYML: "      - name: catalog-all-in-one",
			wantRetry:   "/test catalog-all-in-one branch:{branch}",
		}),
	)

	type orgOverrideCase struct {
		description string
		configOrg   string
		overrideOrg string
		wantOrg     string
	}

	DescribeTable("LoadWithOptions applies github org override",
		func(tc orgOverrideCase) {
			dir := GinkgoT().TempDir()
			orchestrator := filepath.Join(dir, "orchestrator.yaml")
			components := filepath.Join(dir, "components.yaml")

			orchestratorYAML := `apiVersion: porch/v1
kind: ReleaseOrchestration
metadata: {name: test}
connection: {github_org: ` + tc.configOrg + `}
watch: {interval: 30s}
retry:
  max_retries: 1
  backoff: {initial: 1m, multiplier: 1.5, max: 5m}
timeout: {global: 1h}
components_file: ./components.yaml
components:
  - name: catalog
    repo: catalog
    pipelines:
      - name: catalog-all-in-one
`

			Expect(os.WriteFile(orchestrator, []byte(orchestratorYAML), 0o644)).To(Succeed())
			Expect(os.WriteFile(components, []byte(`catalog: {revision: main}`), 0o644)).To(Succeed())

			cfg, err := LoadWithOptions(orchestrator, LoadOptions{
				GitHubOrgOverride: tc.overrideOrg,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Root.Connection.GitHubOrg).To(Equal(tc.wantOrg))
		},
		Entry("uses config org without override", orgOverrideCase{
			description: "no runtime override",
			configOrg:   "AlaudaDevops",
			overrideOrg: "",
			wantOrg:     "AlaudaDevops",
		}),
		Entry("uses runtime override org", orgOverrideCase{
			description: "runtime override takes precedence",
			configOrg:   "AlaudaDevops",
			overrideOrg: "AlaudaDevopsTest",
			wantOrg:     "AlaudaDevopsTest",
		}),
		Entry("allows empty config org when runtime override is provided", orgOverrideCase{
			description: "override satisfies required github org",
			configOrg:   "",
			overrideOrg: "AlaudaDevopsTest",
			wantOrg:     "AlaudaDevopsTest",
		}),
	)
})
