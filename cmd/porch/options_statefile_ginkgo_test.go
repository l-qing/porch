package main

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("defaultWatchStateFileForDir", func() {
	type formatCase struct {
		description string
		workdir     string
		wantExact   string
	}

	DescribeTable("builds expected path format",
		func(tc formatCase) {
			By(tc.description)
			got := defaultWatchStateFileForDir(tc.workdir)
			if tc.wantExact != "" {
				Expect(got).To(Equal(tc.wantExact))
				return
			}

			Expect(got).To(HavePrefix(filepath.Join(os.TempDir(), defaultStateDir) + string(filepath.Separator)))
			Expect(got).To(HaveSuffix(string(filepath.Separator) + defaultStateFile))
			// State file lives in temp/<dir>/<hash>/.porch-state.json.
			Expect(filepath.Base(filepath.Dir(got))).To(HaveLen(16))
		},
		Entry("falls back to global temp file when workdir is empty", formatCase{
			description: "empty workdir",
			workdir:     "",
			wantExact:   filepath.Join(os.TempDir(), defaultStateDir, defaultStateFile),
		}),
		Entry("uses a hashed sub-directory for normal workspace", formatCase{
			description: "hashed workspace path",
			workdir:     filepath.Join(os.TempDir(), "porch-workspace-a"),
		}),
	)

	type stableCase struct {
		description string
		leftDir     string
		rightDir    string
		expectEqual bool
	}

	DescribeTable("is stable and workspace-specific",
		func(tc stableCase) {
			By(tc.description)
			left := defaultWatchStateFileForDir(tc.leftDir)
			right := defaultWatchStateFileForDir(tc.rightDir)
			if tc.expectEqual {
				Expect(left).To(Equal(right))
				return
			}
			Expect(left).NotTo(Equal(right))
		},
		Entry("same workspace maps to same state path", stableCase{
			description: "stable output",
			leftDir:     filepath.Join(os.TempDir(), "porch-workspace-stable"),
			rightDir:    filepath.Join(os.TempDir(), "porch-workspace-stable"),
			expectEqual: true,
		}),
		Entry("different workspaces map to different state paths", stableCase{
			description: "workspace isolation",
			leftDir:     filepath.Join(os.TempDir(), "porch-workspace-left"),
			rightDir:    filepath.Join(os.TempDir(), "porch-workspace-right"),
			expectEqual: false,
		}),
	)
})

var _ = Describe("resolveWatchStateFile", func() {
	type testCase struct {
		description string
		flagValue   string
		viperValue  string
		wantPath    string
		wantSource  string
	}

	DescribeTable("resolves path by priority",
		func(tc testCase) {
			By(tc.description)
			path, source := resolveWatchStateFile(tc.flagValue, tc.viperValue)
			Expect(source).To(Equal(tc.wantSource))
			if tc.wantPath != "" {
				Expect(path).To(Equal(tc.wantPath))
				return
			}
			Expect(path).To(HavePrefix(filepath.Join(os.TempDir(), defaultStateDir) + string(filepath.Separator)))
			Expect(path).To(HaveSuffix(string(filepath.Separator) + defaultStateFile))
		},
		Entry("uses flag first", testCase{
			description: "flag has highest priority",
			flagValue:   "./state.from.flag.json",
			viperValue:  "./state.from.viper.json",
			wantPath:    "./state.from.flag.json",
			wantSource:  stateFileSourceFlag,
		}),
		Entry("uses viper when flag is empty", testCase{
			description: "viper fallback",
			flagValue:   "",
			viperValue:  "./state.from.viper.json",
			wantPath:    "./state.from.viper.json",
			wantSource:  stateFileSourceViper,
		}),
		Entry("uses default temp when neither is set", testCase{
			description: "default temp path",
			flagValue:   "",
			viperValue:  "",
			wantSource:  stateFileSourceDefaultTemp,
		}),
	)
})
