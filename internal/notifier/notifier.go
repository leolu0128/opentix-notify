package notifier

import (
	"context"

	"gocrawler/internal/model"
)

// Notifier 是通知管道的抽象。二階段換 Discord Bot 時實作此 interface 即可。
type Notifier interface {
	Notify(ctx context.Context, e model.Event) error
}
