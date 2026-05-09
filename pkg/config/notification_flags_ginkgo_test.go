package config

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// boolPtr returns a pointer to b, used to simulate "user explicitly set this".
func boolPtr(b bool) *bool { return &b }

var _ = Describe("Notification flag accessors", func() {
	// Each accessor covers nil (default) / true / false. The defaults are
	// asserted positively so a future change to the nil branch fails loudly.
	DescribeTable("ProgressChangesOnlyEnabled is true by default and tracks the pointer",
		func(field *bool, want bool) {
			n := Notification{ProgressChangesOnly: field}
			Expect(n.ProgressChangesOnlyEnabled()).To(Equal(want))
		},
		Entry("nil → true", (*bool)(nil), true),
		Entry("explicit true → true", boolPtr(true), true),
		Entry("explicit false → false", boolPtr(false), false),
	)

	DescribeTable("SuppressSucceededInProgressEnabled is true by default and tracks the pointer",
		func(field *bool, want bool) {
			n := Notification{SuppressSucceededInProgress: field}
			Expect(n.SuppressSucceededInProgressEnabled()).To(Equal(want))
		},
		Entry("nil → true", (*bool)(nil), true),
		Entry("explicit true → true", boolPtr(true), true),
		Entry("explicit false → false", boolPtr(false), false),
	)

	DescribeTable("NotifyComponentSuccessEnabled is true by default and tracks the pointer",
		func(field *bool, want bool) {
			n := Notification{NotifyComponentSuccess: field}
			Expect(n.NotifyComponentSuccessEnabled()).To(Equal(want))
		},
		Entry("nil → true", (*bool)(nil), true),
		Entry("explicit true → true", boolPtr(true), true),
		Entry("explicit false → false", boolPtr(false), false),
	)
})
