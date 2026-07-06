package scraper

import (
	"context"

	"gocrawler/internal/model"
)

// Source 是單一資料源的抽象。新平台(如 tixCraft)實作此 interface 即可接入。
type Source interface {
	Name() string
	Fetch(ctx context.Context) ([]model.Event, error)
}
