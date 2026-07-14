// Package api 提供查詢節目的 REST endpoints(Gin)。
package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"gocrawler/internal/model"
	"gocrawler/internal/storage"
)

// EventStore 是 API 層需要的最小查詢介面;storage.PostgresStore 滿足之。
type EventStore interface {
	ListEvents(ctx context.Context, q, source string, limit, offset int) ([]model.Event, error)
	GetEvent(ctx context.Context, id int64) (*model.Event, error)
}

type handlers struct {
	store EventStore
}

const (
	defaultLimit = 20
	maxLimit     = 100
)

func (h *handlers) listEvents(c *gin.Context) {
	limit, err := strconv.Atoi(c.DefaultQuery("limit", strconv.Itoa(defaultLimit)))
	if err != nil || limit < 1 || limit > maxLimit {
		limit = defaultLimit
	}
	offset, err := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if err != nil || offset < 0 {
		offset = 0
	}
	events, err := h.store.ListEvents(c.Request.Context(),
		c.Query("q"), c.Query("source"), limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	if events == nil {
		events = []model.Event{}
	}
	c.JSON(http.StatusOK, gin.H{"events": events})
}

func (h *handlers) getEvent(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	e, err := h.store.GetEvent(c.Request.Context(), id)
	if errors.Is(err, storage.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "event not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, e)
}

func (h *handlers) healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
