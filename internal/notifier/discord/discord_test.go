package discord

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"gocrawler/internal/model"
)

func TestNotify_SendsContent(t *testing.T) {
	var gotBody []byte
	var gotContentType string
	var gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	start := time.Date(2026, 8, 1, 19, 30, 0, 0, time.FixedZone("CST", 8*3600))
	onSale := time.Date(2026, 7, 1, 12, 0, 0, 0, time.FixedZone("CST", 8*3600))
	n := New(srv.URL)
	err := n.Notify(context.Background(), model.Event{
		Title: "貝多芬交響曲之夜", Venue: "臺北 國家音樂廳",
		URL: "https://www.opentix.life/event/OPX001", StartTime: &start, OnSaleTime: &onSale,
	})
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "application/json", gotContentType)
	var payload struct {
		Content string `json:"content"`
	}
	require.NoError(t, json.Unmarshal(gotBody, &payload))
	require.Contains(t, payload.Content, "貝多芬交響曲之夜")
	require.Contains(t, payload.Content, "臺北 國家音樂廳")
	require.Contains(t, payload.Content, "2026-08-01 19:30")
	require.Contains(t, payload.Content, "2026-07-01 12:00")
	require.Contains(t, payload.Content, "https://www.opentix.life/event/OPX001")
}

func TestNotify_MinimalEvent(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	n := New(srv.URL)
	err := n.Notify(context.Background(), model.Event{Title: "極簡節目", URL: "https://x/1"})
	require.NoError(t, err)

	var payload struct {
		Content string `json:"content"`
	}
	require.NoError(t, json.Unmarshal(gotBody, &payload))
	require.Contains(t, payload.Content, "極簡節目")
	require.Contains(t, payload.Content, "https://x/1")
	require.NotContains(t, payload.Content, "📍", "無場地時不該有場地行")
	require.NotContains(t, payload.Content, "🗓", "無時間時不該有時間行")
	require.NotContains(t, payload.Content, "⏰ 開賣", "無開賣時間時不該有開賣行")
}

func TestNotify_ConvertsToTaipeiTime(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	// UTC 容器場景:UTC 11:30 = 台北 19:30,顯示必須釘在台北時區。
	start := time.Date(2026, 8, 1, 11, 30, 0, 0, time.UTC)
	onSale := time.Date(2026, 7, 1, 4, 0, 0, 0, time.UTC)
	n := New(srv.URL)
	err := n.Notify(context.Background(), model.Event{
		Title: "x", URL: "https://x", StartTime: &start, OnSaleTime: &onSale,
	})
	require.NoError(t, err)

	var payload struct {
		Content string `json:"content"`
	}
	require.NoError(t, json.Unmarshal(gotBody, &payload))
	require.Contains(t, payload.Content, "2026-08-01 19:30")
	require.Contains(t, payload.Content, "2026-07-01 12:00")
}

func TestNotify_TruncatesLongContent(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	long := strings.Repeat("長", 2500)
	n := New(srv.URL)
	err := n.Notify(context.Background(), model.Event{Title: long, URL: "https://x"})
	require.NoError(t, err)

	var payload struct {
		Content string `json:"content"`
	}
	require.NoError(t, json.Unmarshal(gotBody, &payload))
	r := []rune(payload.Content)
	require.LessOrEqual(t, len(r), 2000, "content 不得超過 Discord 2000 字上限")
	require.Equal(t, "…", string(r[len(r)-1]))
}

func TestNotify_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"Must be 2000 or fewer in length."}`))
	}))
	defer srv.Close()

	n := New(srv.URL)
	err := n.Notify(context.Background(), model.Event{Title: "x", URL: "https://x"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "400")
	require.Contains(t, err.Error(), "Must be 2000 or fewer in length.",
		"錯誤應帶回應 body 以利診斷")
}

func TestNotify_CancelledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	n := New(srv.URL)
	err := n.Notify(ctx, model.Event{Title: "x", URL: "https://x"})
	require.Error(t, err)
}

func TestNotify_RateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	n := New(srv.URL)
	err := n.Notify(context.Background(), model.Event{Title: "x", URL: "https://x"})
	require.Error(t, err, "429 應回傳錯誤讓 pipeline 的 retry 處理")
}
