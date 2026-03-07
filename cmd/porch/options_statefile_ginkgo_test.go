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
		withHash    bool
	}

	DescribeTable("builds expected path format",
		func(tc formatCase) {
			By(tc.description)
			got := defaultWatchStateFileForDir(tc.workdir)
			Expect(got).To(HavePrefix(filepath.Join(os.TempDir(), defaultStateDir) + string(filepath.Separator)))
			Expect(got).To(HaveSuffix(string(filepath.Separator) + defaultStateFile))
			runDir := filepath.Base(filepath.Dir(got))
			Expect(runDir).To(HavePrefix("run-"))
			if tc.withHash {
				// State file lives in temp/<dir>/<hash>/run-*/.porch-state.json.
				hashDir := filepath.Base(filepath.Dir(filepath.Dir(got)))
				Expect(hashDir).To(HaveLen(16))
			}
		},
		Entry("uses per-run temp path when workdir is empty", formatCase{
			description: "empty workdir",
			workdir:     "",
			withHash:    false,
		}),
		Entry("uses workspace hash + run dir for normal workspace", formatCase{
			description: "workspace-scoped temp path",
			workdir:     filepath.Join(os.TempDir(), "porch-workspace-a"),
			withHash:    true,
		}),
	)

	type isolationCase struct {
		description string
		workdir     string
	}

	DescribeTable("does not reuse stale state across runs",
		func(tc isolationCase) {
			By(tc.description)
			first := defaultWatchStateFileForDir(tc.workdir)
			second := defaultWatchStateFileForDir(tc.workdir)
			Expect(first).NotTo(Equal(second))
		},
		Entry("same workspace gets fresh path on each invocation", isolationCase{
			description: "fresh state per run",
			workdir:     filepath.Join(os.TempDir(), "porch-workspace-stable"),
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
