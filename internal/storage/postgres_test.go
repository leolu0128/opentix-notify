package storage

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"gocrawler/internal/model"
)

// 需要真實 Postgres:docker compose up -d postgres 後
// $env:TEST_DATABASE_URL = "postgres://gocrawler:gocrawler@localhost:15432/gocrawler?sslmode=disable"
func newTestStore(t *testing.T) *PostgresStore {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	store, err := NewPostgresStore(url)
	require.NoError(t, err)
	// TODO(Task 13): 改用 storage.Migrate 套用真實 migrations,避免 schema 漂移
	_, err = store.db.Exec(`CREATE TABLE IF NOT EXISTS events (
		id BIGSERIAL PRIMARY KEY, source TEXT NOT NULL, source_event_id TEXT NOT NULL,
		title TEXT NOT NULL, url TEXT NOT NULL, venue TEXT NOT NULL DEFAULT '',
		start_time TIMESTAMPTZ, on_sale_time TIMESTAMPTZ, raw JSONB,
		created_at TIMESTAMPTZ NOT NULL DEFAULT now(), UNIQUE (source, source_event_id))`)
	require.NoError(t, err)
	// 防前次 crash 殘留造成誤判:setup 時先清掉 test 來源的舊資料
	_, err = store.db.Exec(`DELETE FROM events WHERE source = 'test'`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = store.db.Exec(`DELETE FROM events WHERE source = 'test'`)
		_ = store.Close()
	})
	return store
}

func TestInsertEvent_NewAndDuplicate(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	e := model.Event{Source: "test", SourceEventID: "e1", Title: "節目一", URL: "https://x/1"}

	inserted, err := store.InsertEvent(ctx, e)
	require.NoError(t, err)
	require.True(t, inserted, "first insert should report inserted")

	inserted, err = store.InsertEvent(ctx, e)
	require.NoError(t, err)
	require.False(t, inserted, "duplicate insert should report not inserted")

	// 覆蓋 nullableRaw 非空分支與 JSONB 驅動層行為
	withRaw := model.Event{
		Source: "test", SourceEventID: "e2", Title: "含原始資料", URL: "https://x/2",
		Raw: json.RawMessage(`{"k":"v"}`),
	}
	inserted, err = store.InsertEvent(ctx, withRaw)
	require.NoError(t, err)
	require.True(t, inserted, "insert with raw JSONB should report inserted")
}

func TestListEvents_FilterAndPagination(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	for _, e := range []model.Event{
		{Source: "test", SourceEventID: "l1", Title: "交響音樂會", URL: "https://x/1"},
		{Source: "test", SourceEventID: "l2", Title: "鋼琴獨奏", URL: "https://x/2"},
		{Source: "test", SourceEventID: "l3", Title: "交響傑作選", URL: "https://x/3"},
	} {
		_, err := store.InsertEvent(ctx, e)
		require.NoError(t, err)
	}

	got, err := store.ListEvents(ctx, "交響", "test", 10, 0)
	require.NoError(t, err)
	require.Len(t, got, 2)

	got, err = store.ListEvents(ctx, "", "test", 2, 0)
	require.NoError(t, err)
	require.Len(t, got, 2)
	// ORDER BY id DESC:無 offset 時第一筆是最後插入的 l3
	require.Equal(t, "l3", got[0].SourceEventID)

	// offset=1 跳過 l3,第一筆應為 l2
	got, err = store.ListEvents(ctx, "", "test", 2, 1)
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, "l2", got[0].SourceEventID)
}

func TestListEvents_EscapesLikeWildcards(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	for _, e := range []model.Event{
		{Source: "test", SourceEventID: "w1", Title: "特價50%場", URL: "https://x/w1"},
		{Source: "test", SourceEventID: "w2", Title: "特價500場", URL: "https://x/w2"},
		{Source: "test", SourceEventID: "w3", Title: "snake_case工作坊", URL: "https://x/w3"},
		{Source: "test", SourceEventID: "w4", Title: "snakeXcase工作坊", URL: "https://x/w4"},
		{Source: "test", SourceEventID: "w5", Title: `反斜線\場`, URL: "https://x/w5"},
	} {
		_, err := store.InsertEvent(ctx, e)
		require.NoError(t, err)
	}

	// q 內的 % 應為字面字元:"50%" 只命中含字面 "50%" 的 w1,不得萬用匹配 "500"
	got, err := store.ListEvents(ctx, "50%", "test", 10, 0)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "w1", got[0].SourceEventID)

	// 單獨 "%" 不得等同列出全部,只命中標題含字面 % 的 w1
	got, err = store.ListEvents(ctx, "%", "test", 10, 0)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "w1", got[0].SourceEventID)

	// q 內的 _ 應為字面字元,不得匹配任意單一字元(w4 的 "X")
	got, err = store.ListEvents(ctx, "snake_case", "test", 10, 0)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "w3", got[0].SourceEventID)

	// q 以 \ 結尾不得讓查詢靜默回空,應命中含字面 `反斜線\` 的 w5
	got, err = store.ListEvents(ctx, `反斜線\`, "test", 10, 0)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "w5", got[0].SourceEventID)
}

func TestGetEvent(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	_, err := store.InsertEvent(ctx, model.Event{Source: "test", SourceEventID: "g1", Title: "查詢測試", URL: "https://x/g1"})
	require.NoError(t, err)

	list, err := store.ListEvents(ctx, "查詢測試", "test", 1, 0)
	require.NoError(t, err)
	require.Len(t, list, 1)

	got, err := store.GetEvent(ctx, list[0].ID)
	require.NoError(t, err)
	require.Equal(t, "查詢測試", got.Title)

	_, err = store.GetEvent(ctx, -1)
	require.ErrorIs(t, err, ErrNotFound)
}
