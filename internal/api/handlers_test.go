package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"gocrawler/internal/model"
	"gocrawler/internal/storage"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

type fakeStore struct {
	events   []model.Event
	err      error
	gotLimit int
}

func (f *fakeStore) ListEvents(ctx context.Context, q, source string, limit, offset int) ([]model.Event, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.gotLimit = limit
	return f.events, nil
}

func (f *fakeStore) GetEvent(ctx context.Context, id int64) (*model.Event, error) {
	if f.err != nil {
		return nil, f.err
	}
	for _, e := range f.events {
		if e.ID == id {
			return &e, nil
		}
	}
	return nil, storage.ErrNotFound
}

func TestListEvents(t *testing.T) {
	store := &fakeStore{events: []model.Event{{ID: 1, Title: "交響音樂會"}}}
	r := NewRouter(store)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/events?q=交響", nil))

	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Events []model.Event `json:"events"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Events, 1)
}

func TestGetEvent_Found(t *testing.T) {
	store := &fakeStore{events: []model.Event{{ID: 42, Title: "找得到"}}}
	r := NewRouter(store)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/events/42", nil))
	require.Equal(t, http.StatusOK, w.Code)
}

func TestGetEvent_NotFound(t *testing.T) {
	store := &fakeStore{}
	r := NewRouter(store)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/events/999", nil))
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetEvent_BadID(t *testing.T) {
	store := &fakeStore{}
	r := NewRouter(store)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/events/abc", nil))
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHealthz(t *testing.T) {
	r := NewRouter(&fakeStore{})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	require.Equal(t, http.StatusOK, w.Code)
}

func TestListEvents_Empty(t *testing.T) {
	store := &fakeStore{}
	r := NewRouter(store)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/events", nil))

	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"events":[]`)
}

func TestListEvents_StoreError(t *testing.T) {
	store := &fakeStore{err: errors.New("pg: connection refused")}
	r := NewRouter(store)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/events", nil))

	require.Equal(t, http.StatusInternalServerError, w.Code)
	require.NotContains(t, w.Body.String(), "connection refused")
}

func TestGetEvent_StoreError(t *testing.T) {
	store := &fakeStore{err: errors.New("pg: connection refused")}
	r := NewRouter(store)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/events/1", nil))

	require.Equal(t, http.StatusInternalServerError, w.Code)
	require.NotContains(t, w.Body.String(), "connection refused")
}

func TestListEvents_LimitClamped(t *testing.T) {
	store := &fakeStore{}
	r := NewRouter(store)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/events?limit=999", nil))

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, maxLimit, store.gotLimit)
}
