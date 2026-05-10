package config

import (
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// snapshotTZ captures and restores the TZ env var around each spec so the
// ResolveLocation tests stay deterministic regardless of the developer's
// shell environment.
func snapshotTZ() {
	prev, had := os.LookupEnv("TZ")
	DeferCleanup(func() {
		if had {
			_ = os.Setenv("TZ", prev)
		} else {
			_ = os.Unsetenv("TZ")
		}
	})
}

var _ = Describe("Notification.ResolveLocation", func() {
	BeforeEach(func() {
		snapshotTZ()
		_ = os.Unsetenv("TZ")
	})

	It("uses Asia/Shanghai when TZ env and YAML field are empty", func() {
		loc, name, warn := Notification{}.ResolveLocation()
		Expect(warn).NotTo(HaveOccurred())
		Expect(name).To(Equal("Asia/Shanghai"))
		Expect(loc).NotTo(BeNil())
		// Beijing time is permanently +08:00.
		_, offset := time.Now().In(loc).Zone()
		Expect(offset).To(Equal(8 * 60 * 60))
	})

	It("honors the YAML field when TZ env is empty", func() {
		loc, name, warn := Notification{Timezone: "UTC"}.ResolveLocation()
		Expect(warn).NotTo(HaveOccurred())
		Expect(name).To(Equal("UTC"))
		Expect(loc).To(Equal(time.UTC))
	})

	It("prefers TZ env over the YAML field", func() {
		Expect(os.Setenv("TZ", "UTC")).To(Succeed())
		loc, name, warn := Notification{Timezone: "Asia/Shanghai"}.ResolveLocation()
		Expect(warn).NotTo(HaveOccurred())
		Expect(name).To(Equal("UTC"))
		Expect(loc).To(Equal(time.UTC))
	})

	It("falls through to YAML when TZ env is invalid and surfaces a warning", func() {
		Expect(os.Setenv("TZ", "Not/AZone")).To(Succeed())
		loc, name, warn := Notification{Timezone: "UTC"}.ResolveLocation()
		Expect(warn).To(HaveOccurred())
		Expect(warn.Error()).To(ContainSubstring("TZ env"))
		Expect(name).To(Equal("UTC"))
		Expect(loc).To(Equal(time.UTC))
	})

	It("falls through to the default when YAML is invalid and surfaces a warning", func() {
		loc, name, warn := Notification{Timezone: "Not/AZone"}.ResolveLocation()
		Expect(warn).To(HaveOccurred())
		Expect(warn.Error()).To(ContainSubstring("notification.timezone"))
		Expect(name).To(Equal("Asia/Shanghai"))
		Expect(loc).NotTo(BeNil())
	})

	It("trims whitespace around the TZ env value before parsing", func() {
		Expect(os.Setenv("TZ", "  UTC  ")).To(Succeed())
		loc, name, warn := Notification{}.ResolveLocation()
		Expect(warn).NotTo(HaveOccurred())
		Expect(name).To(Equal("UTC"))
		Expect(loc).To(Equal(time.UTC))
	})

	It("trims whitespace around the YAML field before parsing", func() {
		loc, name, warn := Notification{Timezone: "  UTC  "}.ResolveLocation()
		Expect(warn).NotTo(HaveOccurred())
		Expect(name).To(Equal("UTC"))
		Expect(loc).To(Equal(time.UTC))
	})

	It("falls through to the default when both overrides are invalid and reports the first error", func() {
		Expect(os.Setenv("TZ", "Not/AZone")).To(Succeed())
		loc, name, warn := Notification{Timezone: "Also/Bogus"}.ResolveLocation()
		Expect(warn).To(HaveOccurred())
		// Resolution priority is TZ > YAML, so the surfaced error must be the
		// TZ failure even though the YAML override also failed afterwards.
		Expect(warn.Error()).To(ContainSubstring("TZ env"))
		Expect(name).To(Equal("Asia/Shanghai"))
		Expect(loc).NotTo(BeNil())
	})
})
