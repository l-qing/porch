package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWecomNotify(t *testing.T) {
	hit := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer ts.Close()

	n := NewWecom(ts.URL, []string{"all_succeeded"})
	if err := n.Notify(context.Background(), "all_succeeded", "ok"); err != nil {
		t.Fatalf("Notify error: %v", err)
	}
	if !hit {
		t.Fatal("expected webhook to be called")
	}
}

func TestWecomNotifyDisabledEvent(t *testing.T) {
	hit := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer ts.Close()

	n := NewWecom(ts.URL, []string{"all_succeeded"})
	if err := n.Notify(context.Background(), "component_exhausted", "skip"); err != nil {
		t.Fatalf("Notify error: %v", err)
	}
	if hit {
		t.Fatal("webhook should not be called for disabled event")
	}
}

func TestWecomNotifyMarkdownPayload(t *testing.T) {
	hit := false
	var payload map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer ts.Close()

	n := NewWecom(ts.URL, []string{"progress_report"})
	if err := n.NotifyMarkdown(context.Background(), "progress_report", "hello"); err != nil {
		t.Fatalf("NotifyMarkdown error: %v", err)
	}
	if !hit {
		t.Fatal("expected webhook to be called")
	}
	if got := payload["msgtype"]; got != "markdown_v2" {
		t.Fatalf("msgtype = %v, want markdown_v2", got)
	}
}

func TestWecomNotifyErrCode(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"errcode":40058,"errmsg":"content exceed max length"}`))
	}))
	defer ts.Close()

	n := NewWecom(ts.URL, []string{"all_succeeded"})
	err := n.Notify(context.Background(), "all_succeeded", "x")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "errcode 40058") {
		t.Fatalf("error = %v, want errcode in message", err)
	}
}
