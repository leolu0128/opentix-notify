package api

import "github.com/gin-gonic/gin"

// NewRouter 建立含全部路由的 Gin engine。
func NewRouter(store EventStore) *gin.Engine {
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())

	h := &handlers{store: store}
	r.GET("/events", h.listEvents)
	r.GET("/events/:id", h.getEvent)
	r.GET("/healthz", h.healthz)
	return r
}
