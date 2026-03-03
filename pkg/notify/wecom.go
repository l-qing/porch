package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Wecom struct {
	webhook string
	events  map[string]struct{}
	client  *http.Client
}

func NewWecom(webhook string, events []string) *Wecom {
	set := map[string]struct{}{}
	for _, e := range events {
		set[e] = struct{}{}
	}
	return &Wecom{
		webhook: webhook,
		events:  set,
		client:  &http.Client{Timeout: 10 * time.Second},
	}
}

func (w *Wecom) Enabled() bool {
	return strings.TrimSpace(w.webhook) != ""
}

func (w *Wecom) Notify(ctx context.Context, event, text string) error {
	if !w.Enabled() {
		return nil
	}
	if _, ok := w.events[event]; !ok {
		return nil
	}
	return w.send(ctx, map[string]any{
		"msgtype": "text",
		"text":    map[string]string{"content": text},
	})
}

func (w *Wecom) NotifyMarkdown(ctx context.Context, event, content string) error {
	if !w.Enabled() {
		return nil
	}
	if _, ok := w.events[event]; !ok {
		return nil
	}
	return w.send(ctx, map[string]any{
		"msgtype":     "markdown_v2",
		"markdown_v2": map[string]string{"content": content},
	})
}

func (w *Wecom) send(ctx context.Context, payload map[string]any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal wecom payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.webhook, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create wecom request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("post wecom webhook: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("wecom webhook returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	bodyText := strings.TrimSpace(string(respBody))
	if bodyText == "" {
		return nil
	}

	var result struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("decode wecom webhook response: %w", err)
	}
	if result.ErrCode != 0 {
		return fmt.Errorf("wecom webhook returned errcode %d: %s", result.ErrCode, result.ErrMsg)
	}
	return nil
}
