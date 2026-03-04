package gh

import (
	"context"
	"errors"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Client retry behavior", func() {
	type testCase struct {
		description      string
		stderr           string
		retryBackoff     []time.Duration
		expectError      bool
		expectedAttempts int
	}

	DescribeTable("ListBranches retry policy",
		func(tc testCase) {
			By(tc.description)
			attempts := 0
			client := NewClient("TestGroup", fakeRunner{fn: func(args ...string) ([]byte, []byte, error) {
				attempts++
				if attempts == 1 {
					return nil, []byte(tc.stderr), errors.New("exit status 1")
				}
				return []byte("main\nrelease-1.9\n"), nil, nil
			}})
			client.retryBackoff = tc.retryBackoff

			branches, err := client.ListBranches(context.Background(), "repo")

			if tc.expectError {
				Expect(err).To(HaveOccurred())
				Expect(strings.ToLower(err.Error())).To(ContainSubstring(strings.ToLower(tc.stderr)))
				Expect(branches).To(BeNil())
			} else {
				Expect(err).NotTo(HaveOccurred())
				Expect(branches).To(Equal([]string{"main", "release-1.9"}))
			}
			Expect(attempts).To(Equal(tc.expectedAttempts))
		},
		Entry("retries transient 502 and succeeds", testCase{
			description:      "should retry on bad gateway",
			stderr:           "gh: Bad Gateway (HTTP 502)",
			retryBackoff:     []time.Duration{0},
			expectError:      false,
			expectedAttempts: 2,
		}),
		Entry("does not retry non-transient 403", testCase{
			description:      "should fail immediately on forbidden",
			stderr:           "gh: Forbidden (HTTP 403)",
			retryBackoff:     []time.Duration{0, 0, 0},
			expectError:      true,
			expectedAttempts: 1,
		}),
	)
})
