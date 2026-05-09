package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"time"

	"porch/pkg/notify"
	pipestatus "porch/pkg/pipeline"
	"porch/pkg/tui"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// rowFor builds a tui.Row for a given pipeline keeping only the fields the
// signature filter cares about (Component / Pipeline lookups). Other display
// fields are intentionally ignored by filterChangedRows / commitProgressSignatures.
func rowFor(component, pipeline string) tui.Row {
	return tui.Row{Component: component, Pipeline: pipeline}
}

// trackedFor wires a single (component, pipeline) into the tracked map used by
// the helpers under test. Status / fields are mutated in-place per scenario.
func trackedFor(component, pipeline string, p *trackedPipeline) map[string]*trackedComponent {
	return map[string]*trackedComponent{
		component: {
			Name:      component,
			Pipelines: map[string]*trackedPipeline{pipeline: p},
		},
	}
}

var _ = Describe("pipelineProgressSignature", func() {
	base := func() *trackedPipeline {
		t := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
		return &trackedPipeline{
			Name:             "all-in-one",
			Status:           pipestatus.StatusRunning,
			RetryCount:       0,
			RunMismatch:      0,
			PipelineRun:      "run-1",
			LastTransitionAt: &t,
		}
	}

	type sigCase struct {
		description string
		mutate      func(p *trackedPipeline)
		shouldEqual bool
	}

	DescribeTable("detects changes across the recorded fields",
		func(tc sigCase) {
			a := base()
			b := base()
			tc.mutate(b)
			if tc.shouldEqual {
				Expect(pipelineProgressSignature(a)).To(Equal(pipelineProgressSignature(b)))
			} else {
				Expect(pipelineProgressSignature(a)).NotTo(Equal(pipelineProgressSignature(b)))
			}
		},
		Entry("identical fixtures share a signature", sigCase{
			mutate:      func(p *trackedPipeline) {},
			shouldEqual: true,
		}),
		Entry("status transition flips the signature", sigCase{
			mutate:      func(p *trackedPipeline) { p.Status = pipestatus.StatusSucceeded },
			shouldEqual: false,
		}),
		Entry("retry count bump flips the signature", sigCase{
			mutate:      func(p *trackedPipeline) { p.RetryCount = 1 },
			shouldEqual: false,
		}),
		Entry("run mismatch bump flips the signature", sigCase{
			mutate:      func(p *trackedPipeline) { p.RunMismatch = 1 },
			shouldEqual: false,
		}),
		Entry("pipelinerun swap flips the signature", sigCase{
			mutate:      func(p *trackedPipeline) { p.PipelineRun = "run-2" },
			shouldEqual: false,
		}),
		Entry("transition timestamp change flips the signature", sigCase{
			mutate: func(p *trackedPipeline) {
				t := p.LastTransitionAt.Add(time.Minute)
				p.LastTransitionAt = &t
			},
			shouldEqual: false,
		}),
	)
})

var _ = Describe("filterChangedRows + commitProgressSignatures", func() {
	now := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)

	It("returns all rows on the first pass when no signature is cached", func() {
		p := &trackedPipeline{Name: "build", Status: pipestatus.StatusRunning, PipelineRun: "run-1", LastTransitionAt: &now}
		tracked := trackedFor("comp-a", "build", p)
		sig := map[string]string{}

		out := filterChangedRows([]tui.Row{rowFor("comp-a", "build")}, tracked, sig)
		Expect(out).To(HaveLen(1))
	})

	It("filters unchanged rows after a commit and re-includes them on change", func() {
		p := &trackedPipeline{Name: "build", Status: pipestatus.StatusRunning, PipelineRun: "run-1", LastTransitionAt: &now}
		tracked := trackedFor("comp-a", "build", p)
		sig := map[string]string{}
		rows := []tui.Row{rowFor("comp-a", "build")}

		// First tick: emits the row, then commits its signature.
		first := filterChangedRows(rows, tracked, sig)
		Expect(first).To(HaveLen(1))
		commitProgressSignatures(first, tracked, sig)

		// Second tick with no state change: filtered out.
		second := filterChangedRows(rows, tracked, sig)
		Expect(second).To(BeEmpty())

		// Third tick after a status flip: re-included.
		p.Status = pipestatus.StatusSucceeded
		third := filterChangedRows(rows, tracked, sig)
		Expect(third).To(HaveLen(1))
	})

	It("commits separately per pipeline so unrelated rows stay independent", func() {
		pa := &trackedPipeline{Name: "build", Status: pipestatus.StatusRunning, PipelineRun: "run-1", LastTransitionAt: &now}
		pb := &trackedPipeline{Name: "test", Status: pipestatus.StatusRunning, PipelineRun: "run-2", LastTransitionAt: &now}
		tracked := map[string]*trackedComponent{
			"comp-a": {Name: "comp-a", Pipelines: map[string]*trackedPipeline{"build": pa, "test": pb}},
		}
		sig := map[string]string{}
		rows := []tui.Row{rowFor("comp-a", "build"), rowFor("comp-a", "test")}

		commitProgressSignatures(filterChangedRows(rows, tracked, sig), tracked, sig)
		Expect(sig).To(HaveLen(2))

		// Only "test" advances; "build" should be filtered while "test" stays.
		pb.RetryCount = 1
		out := filterChangedRows(rows, tracked, sig)
		Expect(out).To(HaveLen(1))
		Expect(out[0].Pipeline).To(Equal("test"))
	})

	It("drops rows whose component or pipeline is missing from tracked", func() {
		// The progressRows set is built from `tracked` in the same tick, so an
		// orphan row indicates a programmer error. Dropping it keeps filter and
		// commit symmetric — a passed-through row would otherwise re-notify on
		// every tick because commit can never record its signature.
		out := filterChangedRows([]tui.Row{rowFor("ghost", "build")}, map[string]*trackedComponent{}, map[string]string{})
		Expect(out).To(BeEmpty())
	})
})

var _ = Describe("pruneProgressSignaturesNotTracked", func() {
	It("drops keys whose component or pipeline is no longer tracked", func() {
		tracked := map[string]*trackedComponent{
			"comp-a": {
				Name:      "comp-a",
				Pipelines: map[string]*trackedPipeline{"build": {Name: "build"}},
			},
		}
		sig := map[string]string{
			"comp-a/build":  "x",
			"comp-a/deploy": "y", // pipeline gone
			"comp-b/build":  "z", // component gone
			"malformed":     "w", // missing '/'
			"/leading":      "v", // empty component
			"trailing/":     "u", // empty pipeline
		}
		pruneProgressSignaturesNotTracked(sig, tracked)
		Expect(sig).To(HaveLen(1))
		Expect(sig).To(HaveKey("comp-a/build"))
	})

	It("is a no-op on an empty signature map", func() {
		sig := map[string]string{}
		pruneProgressSignaturesNotTracked(sig, map[string]*trackedComponent{})
		Expect(sig).To(BeEmpty())
	})
})

var _ = Describe("evictProgressSignaturesForComponents", func() {
	It("drops every key that belongs to the named components", func() {
		sig := map[string]string{
			"comp-a/build":   "x",
			"comp-a/deploy":  "y",
			"comp-b/build":   "z",
			"comp-c/release": "w",
		}
		evictProgressSignaturesForComponents(sig, []string{"comp-a", "comp-b"})
		Expect(sig).To(HaveLen(1))
		Expect(sig).To(HaveKey("comp-c/release"))
	})

	It("is a no-op when sig or names are empty", func() {
		empty := map[string]string{}
		evictProgressSignaturesForComponents(empty, []string{"comp-a"})
		Expect(empty).To(BeEmpty())

		sig := map[string]string{"comp-a/build": "x"}
		evictProgressSignaturesForComponents(sig, nil)
		Expect(sig).To(HaveLen(1))
	})

	It("does not match component names that are a prefix of an unrelated key", func() {
		// "comp" is a prefix of "comp-a/build" only when followed by '/'.
		// Verify the helper's "name + '/'" boundary keeps unrelated entries.
		sig := map[string]string{"comp-a/build": "x", "comp-alpha/build": "y"}
		evictProgressSignaturesForComponents(sig, []string{"comp-a"})
		Expect(sig).To(HaveKey("comp-alpha/build"))
		Expect(sig).NotTo(HaveKey("comp-a/build"))
	})
})

var _ = Describe("reactivatedFromSuccess", func() {
	now := time.Now().UTC()

	It("returns names whose component is no longer fully succeeded", func() {
		notified := map[string]time.Time{"comp-a": now, "comp-b": now}
		tracked := map[string]*trackedComponent{
			"comp-a": newTrackedSingle("comp-a", pipestatus.StatusSucceeded, timePtr(now), timePtr(now)),
			"comp-b": newTrackedSingle("comp-b", pipestatus.StatusRunning, timePtr(now), nil),
		}
		Expect(reactivatedFromSuccess(notified, tracked)).To(ConsistOf("comp-b"))
	})

	It("returns names whose component vanished from tracked", func() {
		notified := map[string]time.Time{"gone": now}
		Expect(reactivatedFromSuccess(notified, map[string]*trackedComponent{})).To(ConsistOf("gone"))
	})

	It("returns nil when nothing has changed", func() {
		notified := map[string]time.Time{"comp-a": now}
		tracked := map[string]*trackedComponent{
			"comp-a": newTrackedSingle("comp-a", pipestatus.StatusSucceeded, timePtr(now), timePtr(now)),
		}
		Expect(reactivatedFromSuccess(notified, tracked)).To(BeEmpty())
	})
})

var _ = Describe("notifyAndCommitProgress", func() {
	now := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)

	It("commits signatures even when wecom returns an HTTP error", func() {
		// Spin up a server that always 500s. Pin the contract: a transient
		// delivery failure must still advance the signature cache so the next
		// tick does not retransmit the same content.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		}))
		defer srv.Close()

		wecom := notify.NewWecom(srv.URL, []string{notify.EventProgressReport})
		p := &trackedPipeline{Name: "build", Status: pipestatus.StatusRunning, PipelineRun: "run-1", LastTransitionAt: &now}
		tracked := trackedFor("comp-a", "build", p)
		sig := map[string]string{}
		rows := []tui.Row{rowFor("comp-a", "build")}
		build := func(chunk []tui.Row, page, total int) string { return "stub" }

		err := notifyAndCommitProgress(context.Background(), wecom, 12, rows, tracked, sig, build)
		Expect(err).To(HaveOccurred())
		Expect(sig).To(HaveKey("comp-a/build"))

		// Same state on the next tick → filter drops the row, no retransmit.
		Expect(filterChangedRows(rows, tracked, sig)).To(BeEmpty())
	})

	It("does not write signatures when sig is nil (changesOnly=false fallback)", func() {
		// Sanity: passing nil sig from the caller bypasses commit so old
		// behavior is preserved when the new flag is opted out.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
		}))
		defer srv.Close()

		wecom := notify.NewWecom(srv.URL, []string{notify.EventProgressReport})
		p := &trackedPipeline{Name: "build", Status: pipestatus.StatusRunning, PipelineRun: "run-1", LastTransitionAt: &now}
		tracked := trackedFor("comp-a", "build", p)
		rows := []tui.Row{rowFor("comp-a", "build")}
		build := func(chunk []tui.Row, page, total int) string { return "stub" }

		err := notifyAndCommitProgress(context.Background(), wecom, 12, rows, tracked, nil, build)
		Expect(err).ToNot(HaveOccurred())
	})
})

var _ = Describe("seedProgressSignaturesForNotified", func() {
	now := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)

	It("populates signatures for every pipeline of every notified component", func() {
		tracked := map[string]*trackedComponent{
			"comp-a": {
				Name: "comp-a",
				Pipelines: map[string]*trackedPipeline{
					"build":  {Name: "build", Status: pipestatus.StatusSucceeded, PipelineRun: "run-1", LastTransitionAt: &now},
					"deploy": {Name: "deploy", Status: pipestatus.StatusSucceeded, PipelineRun: "run-2", LastTransitionAt: &now},
				},
			},
			"comp-b": {
				Name: "comp-b",
				Pipelines: map[string]*trackedPipeline{
					"build": {Name: "build", Status: pipestatus.StatusRunning, PipelineRun: "run-3", LastTransitionAt: &now},
				},
			},
		}
		sig := map[string]string{}
		notified := map[string]time.Time{"comp-a": now}

		seedProgressSignaturesForNotified(sig, tracked, notified)
		Expect(sig).To(HaveLen(2))
		Expect(sig).To(HaveKey("comp-a/build"))
		Expect(sig).To(HaveKey("comp-a/deploy"))
		Expect(sig).NotTo(HaveKey("comp-b/build"))
	})
})
