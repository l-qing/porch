package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("watch minimal config mode", func() {
	type eligibilityCase struct {
		description string
		opts        watchOptions
		loadErr     error
		want        bool
	}

	DescribeTable("shouldUseWatchMinimalConfig",
		func(tc eligibilityCase) {
			got := shouldUseWatchMinimalConfig(tc.opts, tc.loadErr)
			Expect(got).To(Equal(tc.want))
		},
		Entry("enabled for missing config and required scoped flags", eligibilityCase{
			description: "eligible",
			opts: watchOptions{
				commonOptions: commonOptions{githubOrg: "AlaudaDevops"},
				componentName: "devops-artifact",
				pipelineName:  "devops-package-artifact",
			},
			loadErr: fmt.Errorf("read orchestrator file: %w", os.ErrNotExist),
			want:    true,
		}),
		Entry("disabled when github org is empty", eligibilityCase{
			description: "missing org",
			opts: watchOptions{
				componentName: "devops-artifact",
				pipelineName:  "devops-package-artifact",
			},
			loadErr: fmt.Errorf("read orchestrator file: %w", os.ErrNotExist),
			want:    false,
		}),
		Entry("disabled when component is empty", eligibilityCase{
			description: "missing component",
			opts: watchOptions{
				commonOptions: commonOptions{githubOrg: "AlaudaDevops"},
				pipelineName:  "devops-package-artifact",
			},
			loadErr: fmt.Errorf("read orchestrator file: %w", os.ErrNotExist),
			want:    false,
		}),
		Entry("disabled when pipeline is empty", eligibilityCase{
			description: "missing pipeline",
			opts: watchOptions{
				commonOptions: commonOptions{githubOrg: "AlaudaDevops"},
				componentName: "devops-artifact",
			},
			loadErr: fmt.Errorf("read orchestrator file: %w", os.ErrNotExist),
			want:    false,
		}),
		Entry("disabled when load error is not file-missing", eligibilityCase{
			description: "non not-exist error",
			opts: watchOptions{
				commonOptions: commonOptions{githubOrg: "AlaudaDevops"},
				componentName: "devops-artifact",
				pipelineName:  "devops-package-artifact",
			},
			loadErr: errors.New("yaml parse failed"),
			want:    false,
		}),
	)

	type loadCase struct {
		description   string
		opts          watchOptions
		wantErrSubstr string
	}

	DescribeTable("loadRuntimeConfigForWatch",
		func(tc loadCase) {
			opts := tc.opts
			if strings.TrimSpace(opts.commonOptions.configPath) == "" {
				opts.commonOptions.configPath = filepath.Join(GinkgoT().TempDir(), "missing.yaml")
			}
			cfg, err := loadRuntimeConfigForWatch(opts)
			if tc.wantErrSubstr != "" {
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(tc.wantErrSubstr))
				return
			}

			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Root.Connection.GitHubOrg).To(Equal("AlaudaDevops"))
			Expect(cfg.Root.Watch.Interval.Duration).To(Equal(30 * time.Second))
			Expect(cfg.Root.Retry.MaxRetries).To(Equal(5))
			Expect(cfg.Root.Timeout.Global.Duration).To(Equal(12 * time.Hour))
			Expect(cfg.Components).To(BeEmpty())
		},
		Entry("falls back to built-in minimal config when file is missing", loadCase{
			description: "fallback enabled",
			opts: watchOptions{
				commonOptions: commonOptions{
					githubOrg: "AlaudaDevops",
				},
				componentName: "devops-artifact",
				pipelineName:  "devops-package-artifact",
			},
		}),
		Entry("keeps original error when minimal-mode conditions are incomplete", loadCase{
			description: "fallback disabled",
			opts: watchOptions{
				commonOptions: commonOptions{
					githubOrg: "AlaudaDevops",
				},
				componentName: "devops-artifact",
			},
			wantErrSubstr: "read orchestrator file",
		}),
	)
})
