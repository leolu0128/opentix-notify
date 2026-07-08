// Package discord 以 Discord Webhook 實作 notifier.Notifier(單向推播,不需 Bot)。
package discord

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"gocrawler/internal/model"
)

// Webhook 對單一 Discord webhook URL 發送通知。
type Webhook struct {
	url    string
	client *http.Client
}

// New 建立 Discord Webhook notifier。
func New(webhookURL string) *Webhook {
	return &Webhook{
		url:    webhookURL,
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

// Notify 把節目資訊組成一則訊息 POST 到 webhook。
// 非 2xx 一律回傳錯誤,重試策略由呼叫端(pipeline 的 retry)決定。
func (w *Webhook) Notify(ctx context.Context, e model.Event) error {
	var b strings.Builder
	fmt.Fprintf(&b, "🎫 新節目上架:%s", e.Title)
	if e.Venue != "" {
		fmt.Fprintf(&b, "\n📍 %s", e.Venue)
	}
	if e.StartTime != nil {
		fmt.Fprintf(&b, "\n🗓 %s", e.StartTime.Format("2006-01-02 15:04"))
	}
	if e.OnSaleTime != nil {
		fmt.Fprintf(&b, "\n🎫開賣:%s", e.OnSaleTime.Format("2006-01-02 15:04"))
	}
	b.WriteString("\n" + e.URL)

	body, err := json.Marshal(map[string]string{"content": b.String()})
	if err != nil {
		return fmt.Errorf("discord: marshal payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("discord: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("discord: request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("discord: unexpected status %d", resp.StatusCode)
	}
	return nil
}
