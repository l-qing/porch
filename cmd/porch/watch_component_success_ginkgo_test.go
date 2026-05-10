package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	"porch/pkg/config"
	"porch/pkg/notify"
	pipestatus "porch/pkg/pipeline"
	"porch/pkg/tui"

	"github.com/sirupsen/logrus"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// timePtr returns a pointer to t for inline construction in test fixtures.
func timePtr(t time.Time) *time.Time { return &t }

// boolPtr is the Notification toggle helper used to simulate "user set false".
func boolPtr(b bool) *bool { return &b }

func newTrackedSingle(name string, status pipestatus.Status, started, completed *time.Time) *trackedComponent {
	return &trackedComponent{
		Name:   name,
		Repo:   "demo-repo",
		Branch: "main",
		Pipelines: map[string]*trackedPipeline{
			"all-in-one": {
				Name:        "all-in-one",
				Status:      status,
				StartedAt:   started,
				CompletedAt: completed,
				PipelineRun: name + "-run",
			},
		},
	}
}

// captureWecom spins up an httptest server that records every Wecom payload.
type captureWecom struct {
	server *httptest.Server
	mu     sync.Mutex
	calls  []string // markdown contents in arrival order
}

func newCaptureWecom() *captureWecom {
	cw := &captureWecom{}
	cw.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var payload struct {
			MsgType    string `json:"msgtype"`
			MarkdownV2 struct {
				Content string `json:"content"`
			} `json:"markdown_v2"`
			Markdown struct {
				Content string `json:"content"`
			} `json:"markdown"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		content := payload.MarkdownV2.Content
		if content == "" {
			content = payload.Markdown.Content
		}
		cw.mu.Lock()
		cw.calls = append(cw.calls, content)
		cw.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	return cw
}

func (cw *captureWecom) snapshot() []string {
	cw.mu.Lock()
	defer cw.mu.Unlock()
	out := make([]string, len(cw.calls))
	copy(out, cw.calls)
	return out
}

func (cw *captureWecom) close() { cw.server.Close() }

var _ = Describe("componentSucceeded helpers", func() {
	now := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)

	DescribeTable("componentSucceeded",
		func(c *trackedComponent, want bool) {
			Expect(componentSucceeded(c)).To(Equal(want))
		},
		Entry("nil component is not succeeded", nil, false),
		Entry("empty pipelines is not succeeded", &trackedComponent{Name: "empty"}, false),
		Entry("mixed statuses is not succeeded", newTrackedSingle("mix", pipestatus.StatusRunning, nil, nil), false),
		Entry("all succeeded is succeeded", newTrackedSingle("ok", pipestatus.StatusSucceeded, timePtr(now), timePtr(now.Add(time.Minute))), true),
	)

	It("componentStartedAt picks earliest StartedAt and falls back", func() {
		earlier := now.Add(-2 * time.Minute)
		later := now.Add(-1 * time.Minute)
		c := &trackedComponent{
			Name: "c",
			Pipelines: map[string]*trackedPipeline{
				"a": {Name: "a", StartedAt: timePtr(later)},
				"b": {Name: "b", StartedAt: timePtr(earlier)},
				"c": {Name: "c"}, // missing — must be ignored
			},
		}
		Expect(componentStartedAt(c, now)).To(Equal(earlier))
		Expect(componentStartedAt(&trackedComponent{Name: "x"}, now)).To(Equal(now))
	})

	It("componentCompletedAt picks latest CompletedAt and falls back to now", func() {
		early := now.Add(time.Minute)
		late := now.Add(5 * time.Minute)
		c := &trackedComponent{
			Name: "c",
			Pipelines: map[string]*trackedPipeline{
				"a": {Name: "a", CompletedAt: timePtr(early)},
				"b": {Name: "b", CompletedAt: timePtr(late)},
			},
		}
		Expect(componentCompletedAt(c)).To(Equal(late))
		got := componentCompletedAt(&trackedComponent{Name: "x", Pipelines: map[string]*trackedPipeline{"p": {Name: "p"}}})
		Expect(got.IsZero()).To(BeFalse())
	})
})

var _ = Describe("inFlightRows / componentRows", func() {
	now := time.Now().UTC()
	tracked := map[string]*trackedComponent{
		"comp-a": newTrackedSingle("comp-a", pipestatus.StatusSucceeded, timePtr(now.Add(-time.Hour)), timePtr(now.Add(-time.Minute))),
		"comp-b": newTrackedSingle("comp-b", pipestatus.StatusRunning, timePtr(now.Add(-30*time.Minute)), nil),
	}
	conn := config.Connection{}

	It("inFlightRows skips notified components", func() {
		notified := map[string]time.Time{"comp-a": now}
		rows := inFlightRows(tracked, conn, notified)
		Expect(rows).To(HaveLen(1))
		Expect(rows[0].Component).To(Equal("comp-b"))
	})

	It("inFlightRows returns all rows when notified is empty", func() {
		rows := inFlightRows(tracked, conn, nil)
		Expect(rows).To(HaveLen(2))
	})

	It("componentRows yields rows for one component only", func() {
		rows := componentRows(tracked["comp-a"], conn)
		Expect(rows).To(HaveLen(1))
		Expect(rows[0].Component).To(Equal("comp-a"))
		Expect(rows[0].Status).To(Equal(pipestatus.StatusSucceeded))
	})
})

var _ = Describe("Notification toggle helpers", func() {
	DescribeTable("NotifyComponentSuccessEnabled",
		func(field *bool, want bool) {
			n := config.Notification{NotifyComponentSuccess: field}
			Expect(n.NotifyComponentSuccessEnabled()).To(Equal(want))
		},
		Entry("nil defaults to true", nil, true),
		Entry("explicit true", boolPtr(true), true),
		Entry("explicit false", boolPtr(false), false),
	)

	DescribeTable("SuppressSucceededInProgressEnabled",
		func(field *bool, want bool) {
			n := config.Notification{SuppressSucceededInProgress: field}
			Expect(n.SuppressSucceededInProgressEnabled()).To(Equal(want))
		},
		Entry("nil defaults to true", nil, true),
		Entry("explicit true", boolPtr(true), true),
		Entry("explicit false", boolPtr(false), false),
	)
})

var _ = Describe("buildComponentSuccessMarkdown", func() {
	It("includes component, branch, start, finish, elapsed and table", func() {
		started := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
		finished := started.Add(3*time.Minute + 7*time.Second)
		rows := []tui.Row{{Component: "comp-a", Branch: "main", Pipeline: "all-in-one", Status: pipestatus.StatusSucceeded}}
		md := buildComponentSuccessMarkdown("comp-a", "main", started, finished, rows, 1, 1)
		Expect(md).To(ContainSubstring("## 组件流水线成功"))
		Expect(md).To(ContainSubstring("**组件**: `comp-a`"))
		Expect(md).To(ContainSubstring("**分支**: `main`"))
		Expect(md).To(ContainSubstring("**开始时间**:"))
		Expect(md).To(ContainSubstring("**完成时间**:"))
		Expect(md).To(ContainSubstring("**耗时**: 3m7s"))
		// Multi-page header should not appear when total==1
		Expect(md).NotTo(ContainSubstring("**分片**"))
	})

	It("emits 分片 header when chunked into multiple parts", func() {
		started := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
		md := buildComponentSuccessMarkdown("comp-a", "main", started, started.Add(time.Minute), nil, 2, 3)
		Expect(md).To(ContainSubstring("**分片**: 2/3"))
	})

	It("renders timestamps with the configured IANA timezone label", func() {
		// Restore the package-level default after the spec so other tests
		// continue to render with whatever runWatch / the process default
		// would produce.
		DeferCleanup(setNotifyLocation, notifyLoc, notifyLocName)

		setNotifyLocation(time.UTC, "UTC")
		started := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
		finished := started.Add(time.Minute)
		md := buildComponentSuccessMarkdown("comp-a", "main", started, finished, nil, 1, 1)
		Expect(md).To(ContainSubstring("2026-05-09 10:00:00 (UTC)"))
		Expect(md).To(ContainSubstring("2026-05-09 10:01:00 (UTC)"))

		shanghai, err := time.LoadLocation("Asia/Shanghai")
		Expect(err).NotTo(HaveOccurred())
		setNotifyLocation(shanghai, "Asia/Shanghai")
		md = buildComponentSuccessMarkdown("comp-a", "main", started, finished, nil, 1, 1)
		Expect(md).To(ContainSubstring("2026-05-09 18:00:00 (Asia/Shanghai)"))
		Expect(md).To(ContainSubstring("2026-05-09 18:01:00 (Asia/Shanghai)"))
	})
})

var _ = Describe("buildProgressMarkdown done counter", func() {
	started := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	reported := started.Add(15 * time.Minute)

	It("renders 已完成 M/N and 在飞 K when totalCount > 0 and inFlightCount > 0", func() {
		md := buildProgressMarkdown(nil, started, reported, 1, 1, 7, 40, 33)
		Expect(md).To(ContainSubstring("**已完成**: 7/40（已单独通知）"))
		Expect(md).To(ContainSubstring("**在飞**: 33"))
	})

	It("omits 在飞 line when inFlightCount == 0 (suppressSucceeded off path)", func() {
		md := buildProgressMarkdown(nil, started, reported, 1, 1, 7, 40, 0)
		Expect(md).To(ContainSubstring("**已完成**: 7/40（已单独通知）"))
		Expect(md).NotTo(ContainSubstring("在飞"))
	})

	It("omits 已完成 / 在飞 lines when totalCount == 0", func() {
		md := buildProgressMarkdown(nil, started, reported, 1, 1, 0, 0, 0)
		Expect(md).NotTo(ContainSubstring("已完成"))
		Expect(md).NotTo(ContainSubstring("在飞"))
	})
})

var _ = Describe("evictReactivatedSuccess", func() {
	It("drops entries whose component is no longer succeeded", func() {
		now := time.Now().UTC()
		notified := map[string]time.Time{
			"comp-a": now,
			"comp-b": now,
		}
		tracked := map[string]*trackedComponent{
			"comp-a": newTrackedSingle("comp-a", pipestatus.StatusSucceeded, timePtr(now), timePtr(now)),
			// comp-b flipped back to running (e.g., new commit triggered a fresh run)
			"comp-b": newTrackedSingle("comp-b", pipestatus.StatusRunning, timePtr(now), nil),
		}
		evicted := evictReactivatedSuccess(notified, tracked)
		Expect(evicted).To(Equal(1))
		Expect(notified).To(HaveKey("comp-a"))
		Expect(notified).NotTo(HaveKey("comp-b"))
	})

	It("drops entries whose component is no longer tracked", func() {
		notified := map[string]time.Time{"gone": time.Now()}
		evicted := evictReactivatedSuccess(notified, map[string]*trackedComponent{})
		Expect(evicted).To(Equal(1))
		Expect(notified).To(BeEmpty())
	})
})

var _ = Describe("notifyComponentFirstSuccesses", func() {
	var (
		cw      *captureWecom
		emitted []watchEventKind
		emit    func(kind watchEventKind, msg string, fields logrus.Fields)
	)

	BeforeEach(func() {
		cw = newCaptureWecom()
		emitted = nil
		emit = func(kind watchEventKind, _ string, _ logrus.Fields) {
			emitted = append(emitted, kind)
		}
	})

	AfterEach(func() {
		cw.close()
	})

	It("notifies once per component and skips on the next tick", func() {
		started := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
		finished := started.Add(5 * time.Minute)
		tracked := map[string]*trackedComponent{
			"comp-a": newTrackedSingle("comp-a", pipestatus.StatusSucceeded, timePtr(started), timePtr(finished)),
		}
		cfg := config.RuntimeConfig{Root: config.Root{Notification: config.Notification{Events: []string{notify.EventComponentSucceeded}}}}
		wecom := notify.NewWecom(cw.server.URL, cfg.Root.Notification.Events)
		notified := map[string]time.Time{}

		notifyComponentFirstSuccesses(context.Background(), wecom, cfg, tracked, notified, started, 12, emit)
		notifyComponentFirstSuccesses(context.Background(), wecom, cfg, tracked, notified, started, 12, emit)

		Expect(cw.snapshot()).To(HaveLen(1))
		Expect(cw.snapshot()[0]).To(ContainSubstring("**组件**: `comp-a`"))
		Expect(cw.snapshot()[0]).To(ContainSubstring("**耗时**: 5m0s"))
		Expect(notified).To(HaveKey("comp-a"))
		Expect(notified["comp-a"]).To(Equal(finished))
	})

	It("re-arms a component after re-activation and reports the new run", func() {
		started := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
		firstFinish := started.Add(2 * time.Minute)
		tracked := map[string]*trackedComponent{
			"comp-a": newTrackedSingle("comp-a", pipestatus.StatusSucceeded, timePtr(started), timePtr(firstFinish)),
		}
		cfg := config.RuntimeConfig{Root: config.Root{Notification: config.Notification{Events: []string{notify.EventComponentSucceeded}}}}
		wecom := notify.NewWecom(cw.server.URL, cfg.Root.Notification.Events)
		notified := map[string]time.Time{}

		// First success → notify once.
		notifyComponentFirstSuccesses(context.Background(), wecom, cfg, tracked, notified, started, 12, emit)
		Expect(cw.snapshot()).To(HaveLen(1))

		// New commit lands → status flips back to running.
		secondStart := firstFinish.Add(time.Minute)
		tracked["comp-a"].Pipelines["all-in-one"].Status = pipestatus.StatusRunning
		tracked["comp-a"].Pipelines["all-in-one"].StartedAt = timePtr(secondStart)
		tracked["comp-a"].Pipelines["all-in-one"].CompletedAt = nil

		evictReactivatedSuccess(notified, tracked)
		Expect(notified).NotTo(HaveKey("comp-a"))

		// Run finishes again with a fresh CompletedAt.
		secondFinish := secondStart.Add(7 * time.Minute)
		tracked["comp-a"].Pipelines["all-in-one"].Status = pipestatus.StatusSucceeded
		tracked["comp-a"].Pipelines["all-in-one"].CompletedAt = timePtr(secondFinish)

		notifyComponentFirstSuccesses(context.Background(), wecom, cfg, tracked, notified, started, 12, emit)

		snap := cw.snapshot()
		Expect(snap).To(HaveLen(2))
		Expect(snap[1]).To(ContainSubstring("**耗时**: 7m0s"))
		Expect(notified["comp-a"]).To(Equal(secondFinish))
	})

	It("respects NotifyComponentSuccess=false (no Wecom call, no bookkeeping)", func() {
		started := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
		tracked := map[string]*trackedComponent{
			"comp-a": newTrackedSingle("comp-a", pipestatus.StatusSucceeded, timePtr(started), timePtr(started.Add(time.Minute))),
		}
		cfg := config.RuntimeConfig{Root: config.Root{Notification: config.Notification{
			Events:                 []string{notify.EventComponentSucceeded},
			NotifyComponentSuccess: boolPtr(false),
		}}}
		wecom := notify.NewWecom(cw.server.URL, cfg.Root.Notification.Events)
		notified := map[string]time.Time{}

		notifyComponentFirstSuccesses(context.Background(), wecom, cfg, tracked, notified, started, 12, emit)

		Expect(cw.snapshot()).To(BeEmpty())
		Expect(notified).To(BeEmpty())
	})

	It("processes components in sorted order (deterministic chunk delivery)", func() {
		started := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
		tracked := map[string]*trackedComponent{
			"zeta":  newTrackedSingle("zeta", pipestatus.StatusSucceeded, timePtr(started), timePtr(started.Add(time.Minute))),
			"alpha": newTrackedSingle("alpha", pipestatus.StatusSucceeded, timePtr(started), timePtr(started.Add(time.Minute))),
			"mike":  newTrackedSingle("mike", pipestatus.StatusSucceeded, timePtr(started), timePtr(started.Add(time.Minute))),
		}
		cfg := config.RuntimeConfig{Root: config.Root{Notification: config.Notification{Events: []string{notify.EventComponentSucceeded}}}}
		wecom := notify.NewWecom(cw.server.URL, cfg.Root.Notification.Events)

		notifyComponentFirstSuccesses(context.Background(), wecom, cfg, tracked, map[string]time.Time{}, started, 12, emit)

		snap := cw.snapshot()
		Expect(snap).To(HaveLen(3))
		// Sorted iteration must place alpha < mike < zeta.
		Expect(snap[0]).To(ContainSubstring("`alpha`"))
		Expect(snap[1]).To(ContainSubstring("`mike`"))
		Expect(snap[2]).To(ContainSubstring("`zeta`"))
	})
})
