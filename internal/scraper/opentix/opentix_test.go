package opentix

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var testCategories = []string{"音樂-管絃樂團", "音樂-合唱"}

// newFixtureServer 依請求 body 的 offset 回傳對應頁的 fixture。
func newFixtureServer(t *testing.T) (*httptest.Server, *[]searchRequest) {
	t.Helper()
	var requests []searchRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req searchRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		requests = append(requests, req)

		file := "testdata/search_page1.json"
		if req.Offset >= 2 {
			file = "testdata/search_page2.json"
		}
		data, err := os.ReadFile(file)
		require.NoError(t, err)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	}))
	return srv, &requests
}

func TestNew_EmptyCategories(t *testing.T) {
	_, err := New("https://example.com", nil)
	require.Error(t, err)
}

func TestFetch_PaginatesAndMapsFields(t *testing.T) {
	srv, requests := newFixtureServer(t)
	defer srv.Close()

	s, err := New(srv.URL, testCategories)
	require.NoError(t, err)
	s.pageDelay = 0

	events, err := s.Fetch(context.Background())
	require.NoError(t, err)
	require.Len(t, events, 3, "兩頁 fixture 共 3 筆")
	require.Len(t, *requests, 2, "hitsCount=3 應該在第二頁後停止")

	// 請求體
	first := (*requests)[0]
	require.Equal(t, "zh-CHT", first.Language)
	require.Equal(t, testCategories, first.CategoryFilter)
	require.Equal(t, "ABOUT_TO_BEGIN", first.SortBy)
	require.Equal(t, 0, first.Offset)
	require.Equal(t, 2, (*requests)[1].Offset, "第二頁用 nextOffset")

	// 欄位對映(第一筆:單一場館)
	e := events[0]
	require.Equal(t, "opentix", e.Source)
	require.Equal(t, "2043631234892812288", e.SourceEventID)
	require.Equal(t, "2026蔚藍之聲~土地之愛", e.Title)
	require.Equal(t, "https://www.opentix.life/event/2043631234892812288", e.URL)
	require.Equal(t, "臺北 國家兩廳院演奏廳", e.Venue)
	require.NotNil(t, e.StartTime)
	require.Equal(t, time.UnixMilli(1783337400000).Unix(), e.StartTime.Unix())
	require.NotNil(t, e.OnSaleTime)
	require.Equal(t, time.UnixMilli(1779076800000).Unix(), e.OnSaleTime.Unix())
	require.NotEmpty(t, e.Raw)

	// 多場館 → places 連接
	require.Equal(t, "高雄、臺中", events[1].Venue)

	// 缺時間欄位 → nil
	require.Nil(t, events[2].StartTime)
	require.Nil(t, events[2].OnSaleTime)
}

func TestFetch_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	s, err := New(srv.URL, testCategories)
	require.NoError(t, err)
	s.pageDelay = 0
	_, err = s.Fetch(context.Background())
	require.Error(t, err)
}

func TestFetch_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	s, err := New(srv.URL, testCategories)
	require.NoError(t, err)
	s.pageDelay = 0
	_, err = s.Fetch(context.Background())
	require.Error(t, err)
}

func TestFetch_StuckNextOffsetStops(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		// nextOffset 永遠是 0(不前進),hitsCount 很大
		_, _ = w.Write([]byte(`{"result":{"hitsCount":1000,"found":[{"source":{"id":"x1","title":"t"}}],"nextOffset":0}}`))
	}))
	defer srv.Close()

	s, err := New(srv.URL, testCategories)
	require.NoError(t, err)
	s.pageDelay = 0
	events, err := s.Fetch(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, calls, "nextOffset 不前進就該停,防無窮迴圈")
	require.Len(t, events, 1)
}

func TestFetch_MaxPagesCap(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req searchRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		calls++
		resp := map[string]any{
			"result": map[string]any{
				"hitsCount":  100000,
				"found":      []map[string]any{{"source": map[string]any{"id": "x", "title": "t"}}},
				"nextOffset": req.Offset + 1,
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	s, err := New(srv.URL, testCategories)
	require.NoError(t, err)
	s.pageDelay = 0
	_, err = s.Fetch(context.Background())
	require.NoError(t, err)
	require.Equal(t, maxPages, calls)
}
