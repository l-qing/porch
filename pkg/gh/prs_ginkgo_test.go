package gh

import (
	"context"
	"errors"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Pull request APIs", func() {
	type pullRequestCase struct {
		description   string
		response      string
		stderr        string
		runErr        error
		wantRef       string
		wantSHA       string
		wantErrSubstr string
	}

	DescribeTable("PullRequest",
		func(tc pullRequestCase) {
			client := NewClient("TestGroup", fakeRunner{fn: func(args ...string) ([]byte, []byte, error) {
				Expect(strings.Join(args, " ")).To(Equal("api repos/TestGroup/repo/pulls/123"))
				if tc.runErr != nil {
					return nil, []byte(tc.stderr), tc.runErr
				}
				return []byte(tc.response), nil, nil
			}})

			pr, err := client.PullRequest(context.Background(), "repo", 123)
			if tc.wantErrSubstr != "" {
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(tc.wantErrSubstr))
				return
			}
			Expect(err).NotTo(HaveOccurred())
			Expect(pr.Number).To(Equal(123))
			Expect(pr.Head.Ref).To(Equal(tc.wantRef))
			Expect(pr.Head.SHA).To(Equal(tc.wantSHA))
		},
		Entry("loads open pull request with head branch and sha", pullRequestCase{
			description: "normal pull request payload",
			response:    `{"number":123,"state":"open","head":{"ref":"feat/add-golang-task","sha":"abc123"}}`,
			wantRef:     "feat/add-golang-task",
			wantSHA:     "abc123",
		}),
		Entry("returns command stderr on gh failure", pullRequestCase{
			description:   "gh api failed",
			stderr:        "forbidden",
			runErr:        errors.New("exit status 1"),
			wantErrSubstr: "forbidden",
		}),
		Entry("validates missing head ref", pullRequestCase{
			description:   "invalid payload",
			response:      `{"number":123,"state":"open","head":{"ref":""}}`,
			wantErrSubstr: "empty pull request head ref",
		}),
	)

	type commentCase struct {
		description   string
		stderr        string
		runErr        error
		wantErrSubstr string
	}

	DescribeTable("CreatePullRequestComment",
		func(tc commentCase) {
			client := NewClient("TestGroup", fakeRunner{fn: func(args ...string) ([]byte, []byte, error) {
				Expect(strings.Join(args, " ")).To(Equal("api repos/TestGroup/repo/issues/123/comments -f body=/test tp-all-in-one branch:main"))
				if tc.runErr != nil {
					return nil, []byte(tc.stderr), tc.runErr
				}
				return nil, nil, nil
			}})

			err := client.CreatePullRequestComment(context.Background(), "repo", 123, "/test tp-all-in-one branch:main")
			if tc.wantErrSubstr != "" {
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(tc.wantErrSubstr))
				return
			}
			Expect(err).NotTo(HaveOccurred())
		},
		Entry("posts issue comment to pull request", commentCase{
			description: "success path",
		}),
		Entry("returns stderr when posting comment fails", commentCase{
			description:   "failure path",
			stderr:        "unprocessable",
			runErr:        errors.New("exit status 1"),
			wantErrSubstr: "unprocessable",
		}),
	)
})
