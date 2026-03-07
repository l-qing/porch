package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"

	"porch/pkg/notify"
	pipestatus "porch/pkg/pipeline"
	"porch/pkg/tui"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("notifyMarkdownInChunks", func() {
	type testCase struct {
		description string
		rows        []tui.Row
		wantOrder   []string
	}

	DescribeTable("sorts globally before chunking",
		func(tc testCase) {
			By(tc.description)

			contents := make([]string, 0, len(tc.wantOrder))
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer r.Body.Close()
				var payload struct {
					MsgType    string `json:"msgtype"`
					MarkdownV2 struct {
						Content string `json:"content"`
					} `json:"markdown_v2"`
				}
				Expect(json.NewDecoder(r.Body).Decode(&payload)).To(Succeed())
				contents = append(contents, strings.TrimSpace(payload.MarkdownV2.Content))
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
			}))
			defer server.Close()

			wecom := notify.NewWecom(server.URL, []string{notify.EventProgressReport})
			err := notifyMarkdownInChunks(
				context.Background(),
				wecom,
				notify.EventProgressReport,
				tc.rows,
				1,
				func(chunk []tui.Row, page, total int) string {
					return chunk[0].Component
				},
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(contents).To(Equal(tc.wantOrder))
		},
		Entry("keeps cross-part order consistent with status/retry priority", testCase{
			description: "global sort then split",
			rows: []tui.Row{
				{Component: "comp-ok", Status: pipestatus.StatusSucceeded, Retries: 0},
				{Component: "comp-run-low", Status: pipestatus.StatusRunning, Retries: 1},
				{Component: "comp-fail", Status: pipestatus.StatusFailed, Retries: 0},
				{Component: "comp-run-high", Status: pipestatus.StatusRunning, Retries: 3},
			},
			wantOrder: []string{
				"comp-fail",
				"comp-run-high",
				"comp-run-low",
				"comp-ok",
			},
		}),
	)
})
