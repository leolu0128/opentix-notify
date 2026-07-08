// Package discord 以 Discord Webhook 實作 notifier.Notifier(單向推播,不需 Bot)。
package discord

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"gocrawler/internal/model"
	"gocrawler/internal/notifier"
)

// 編譯期斷言:*Webhook 必須實作 notifier.Notifier。
var _ notifier.Notifier = (*Webhook)(nil)

// tzTaipei 固定通知顯示時區:使用者在台灣,部署環境可能是 UTC 容器。
var tzTaipei = func() *time.Location {
	if loc, err := time.LoadLocation("Asia/Taipei"); err == nil {
		return loc
	}
	return time.FixedZone("Asia/Taipei", 8*3600)
}()

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
// 非 2xx 一律回傳錯誤,重試策略由呼叫端(pipeline 的 retry)決定;
// 含 429;不讀 Retry-After,交由呼叫端退避。
func (w *Webhook) Notify(ctx context.Context, e model.Event) error {
	var b strings.Builder
	fmt.Fprintf(&b, "🎫 新節目上架:%s", e.Title)
	if e.Venue != "" {
		fmt.Fprintf(&b, "\n📍 %s", e.Venue)
	}
	if e.StartTime != nil {
		fmt.Fprintf(&b, "\n🗓 %s", e.StartTime.In(tzTaipei).Format("2006-01-02 15:04"))
	}
	if e.OnSaleTime != nil {
		fmt.Fprintf(&b, "\n⏰ 開賣:%s", e.OnSaleTime.In(tzTaipei).Format("2006-01-02 15:04"))
	}
	b.WriteString("\n" + e.URL)

	// Discord content 上限 2000 字:超長寧可截斷,也不要整則通知因 400 丟失。
	content := b.String()
	if r := []rune(content); len(r) > 1900 {
		content = string(r[:1900]) + "…"
	}

	body, err := json.Marshal(map[string]string{"content": content})
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
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("discord: unexpected status %d: %s", resp.StatusCode, snippet)
	}
	return nil
}
