package main

import (
	"fmt"
	"strings"
	"time"

	"porch/pkg/config"
	pipestatus "porch/pkg/pipeline"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("seedSucceededBaseline", func() {
	now := time.Date(2026, 5, 10, 14, 0, 0, 0, time.UTC)

	It("seeds only components in Succeeded state and stores their CompletedAt", func() {
		completed := now.Add(-time.Hour)
		tracked := map[string]*trackedComponent{
			"green":  newTrackedSingle("green", pipestatus.StatusSucceeded, timePtr(now.Add(-2*time.Hour)), timePtr(completed)),
			"red":    newTrackedSingle("red", pipestatus.StatusFailed, timePtr(now.Add(-2*time.Hour)), timePtr(now.Add(-90*time.Minute))),
			"flying": newTrackedSingle("flying", pipestatus.StatusRunning, timePtr(now.Add(-15*time.Minute)), nil),
		}
		notified := map[string]time.Time{}

		seeded := seedSucceededBaseline(tracked, notified)

		Expect(seeded).To(ConsistOf("green"))
		Expect(notified).To(HaveLen(1))
		Expect(notified["green"]).To(Equal(completed))
	})

	It("does not overwrite entries that are already notified", func() {
		earlier := now.Add(-3 * time.Hour)
		tracked := map[string]*trackedComponent{
			"green": newTrackedSingle("green", pipestatus.StatusSucceeded, timePtr(now.Add(-2*time.Hour)), timePtr(now.Add(-time.Hour))),
		}
		notified := map[string]time.Time{"green": earlier}

		seeded := seedSucceededBaseline(tracked, notified)

		// Already-notified entries are left untouched: timestamp keeps the
		// pre-existing value and the component is absent from the seeded list.
		Expect(seeded).To(BeEmpty())
		Expect(notified["green"]).To(Equal(earlier))
	})

	It("returns the freshly-seeded component names in sorted order", func() {
		tracked := map[string]*trackedComponent{
			"charlie": newTrackedSingle("charlie", pipestatus.StatusSucceeded, timePtr(now.Add(-time.Hour)), timePtr(now.Add(-30*time.Minute))),
			"alpha":   newTrackedSingle("alpha", pipestatus.StatusSucceeded, timePtr(now.Add(-time.Hour)), timePtr(now.Add(-29*time.Minute))),
			"bravo":   newTrackedSingle("bravo", pipestatus.StatusSucceeded, timePtr(now.Add(-time.Hour)), timePtr(now.Add(-28*time.Minute))),
		}
		notified := map[string]time.Time{}

		seeded := seedSucceededBaseline(tracked, notified)

		Expect(seeded).To(Equal([]string{"alpha", "bravo", "charlie"}))
	})

	It("returns an empty slice when tracked has no Succeeded components", func() {
		tracked := map[string]*trackedComponent{
			"flying": newTrackedSingle("flying", pipestatus.StatusRunning, timePtr(now.Add(-15*time.Minute)), nil),
		}
		notified := map[string]time.Time{}

		seeded := seedSucceededBaseline(tracked, notified)

		Expect(seeded).To(BeEmpty())
		Expect(notified).To(BeEmpty())
	})

	It("removes baseline-silenced rows from inFlightRows under default suppression", func() {
		// Default SuppressSucceededInProgress is true, so the periodic progress
		// table should hide components that the baseline already silenced.
		green := newTrackedSingle("green", pipestatus.StatusSucceeded, timePtr(now.Add(-2*time.Hour)), timePtr(now.Add(-time.Hour)))
		flying := newTrackedSingle("flying", pipestatus.StatusRunning, timePtr(now.Add(-15*time.Minute)), nil)
		tracked := map[string]*trackedComponent{
			"green":  green,
			"flying": flying,
		}
		notified := map[string]time.Time{}

		seedSucceededBaseline(tracked, notified)
		rows := inFlightRows(tracked, config.Connection{}, notified)

		Expect(rows).To(HaveLen(1))
		Expect(rows[0].Component).To(Equal("flying"))
	})

	It("interplays correctly with evictReactivatedSuccess on re-activation", func() {
		comp := newTrackedSingle("green", pipestatus.StatusSucceeded, timePtr(now.Add(-time.Hour)), timePtr(now.Add(-30*time.Minute)))
		tracked := map[string]*trackedComponent{"green": comp}
		notified := map[string]time.Time{}

		// First tick: baseline silences the already-green component.
		seeded := seedSucceededBaseline(tracked, notified)
		Expect(seeded).To(Equal([]string{"green"}))
		Expect(notified).To(HaveKey("green"))

		// Simulate a new commit triggering a fresh run: the pipeline flips
		// back to Running. evictReactivatedSuccess must drop the silenced
		// entry so the next Succeeded transition re-fires the WeCom card.
		comp.Pipelines["all-in-one"].Status = pipestatus.StatusRunning
		comp.Pipelines["all-in-one"].CompletedAt = nil
		evicted := evictReactivatedSuccess(notified, tracked)

		Expect(evicted).To(Equal(1))
		Expect(notified).NotTo(HaveKey("green"))
	})
})

var _ = Describe("formatStartupSilentMessage", func() {
	It("inlines the full list when below the preview limit", func() {
		seeded := []string{"alpha", "bravo", "charlie"}
		msg := formatStartupSilentMessage(seeded)

		Expect(msg).To(ContainSubstring("silenced 3 already-succeeded components: alpha,bravo,charlie"))
		Expect(msg).NotTo(ContainSubstring("first"))
	})

	It("truncates the inlined list past the preview limit and reports the total", func() {
		seeded := make([]string, 0, startupSilentPreviewLimit+5)
		for i := 0; i < startupSilentPreviewLimit+5; i++ {
			seeded = append(seeded, fmt.Sprintf("c%02d", i))
		}
		msg := formatStartupSilentMessage(seeded)

		Expect(msg).To(ContainSubstring(fmt.Sprintf("silenced %d already-succeeded components", len(seeded))))
		Expect(msg).To(ContainSubstring(fmt.Sprintf("first %d:", startupSilentPreviewLimit)))
		Expect(msg).To(ContainSubstring(strings.Join(seeded[:startupSilentPreviewLimit], ",")))
		// The truncated tail must not leak into the preview.
		Expect(msg).NotTo(ContainSubstring(seeded[startupSilentPreviewLimit]))
		Expect(msg).To(HaveSuffix(", ...)"))
	})
})
