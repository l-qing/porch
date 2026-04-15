package config

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("pipeline arg parsing", func() {
	type splitCase struct {
		description string
		input       string
		wantName    string
		wantArgs    string
	}

	DescribeTable("SplitPipelineArg",
		func(tc splitCase) {
			name, args := SplitPipelineArg(tc.input)
			Expect(name).To(Equal(tc.wantName))
			Expect(args).To(Equal(tc.wantArgs))
		},
		Entry("bare name returns empty args", splitCase{
			description: "plain name",
			input:       "catalog-all-in-one",
			wantName:    "catalog-all-in-one",
			wantArgs:    "",
		}),
		Entry("single key=value arg", splitCase{
			description: "one extra arg",
			input:       "catalog-all-e2e-test version_scope=all",
			wantName:    "catalog-all-e2e-test",
			wantArgs:    "version_scope=all",
		}),
		Entry("multiple args preserved verbatim", splitCase{
			description: "multiple extra args",
			input:       "catalog-all-in-one image_build_enabled=false foo=bar",
			wantName:    "catalog-all-in-one",
			wantArgs:    "image_build_enabled=false foo=bar",
		}),
		Entry("trims surrounding whitespace", splitCase{
			description: "leading and trailing whitespace",
			input:       "   name    key=val   ",
			wantName:    "name",
			wantArgs:    "key=val",
		}),
		Entry("tab separator splits correctly", splitCase{
			description: "tab as separator",
			input:       "name\tkey=val",
			wantName:    "name",
			wantArgs:    "key=val",
		}),
		Entry("empty string returns empty pair", splitCase{
			description: "empty input",
			input:       "",
			wantName:    "",
			wantArgs:    "",
		}),
	)

	type commandCase struct {
		description string
		name        string
		extraArgs   string
		want        string
	}

	DescribeTable("DefaultRetryCommandWithArgs",
		func(tc commandCase) {
			Expect(DefaultRetryCommandWithArgs(tc.name, tc.extraArgs)).To(Equal(tc.want))
		},
		Entry("falls back to default when args empty", commandCase{
			description: "no extra args",
			name:        "catalog-all-in-one",
			extraArgs:   "",
			want:        "/test catalog-all-in-one branch:{branch}",
		}),
		Entry("injects args between name and branch selector", commandCase{
			description: "with extra args",
			name:        "catalog-all-in-one",
			extraArgs:   "image_build_enabled=false",
			want:        "/test catalog-all-in-one image_build_enabled=false branch:{branch}",
		}),
		Entry("returns empty when pipeline name missing", commandCase{
			description: "empty name",
			name:        "",
			extraArgs:   "foo=bar",
			want:        "",
		}),
	)
})
