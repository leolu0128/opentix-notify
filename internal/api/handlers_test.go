package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"gocrawler/internal/model"
	"gocrawler/internal/storage"
)

type fakeStore struct {
	events []model.Event
}

func (f *fakeStore) ListEvents(ctx context.Context, q, source string, limit, offset int) ([]model.Event, error) {
	return f.events, nil
}

func (f *fakeStore) GetEvent(ctx context.Context, id int64) (*model.Event, error) {
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
