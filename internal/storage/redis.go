package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// dedupTTL 限制 Redis 記憶體用量;過期後 Postgres UNIQUE 仍擋住重複通知。
const dedupTTL = 90 * 24 * time.Hour

// Deduper 用 Redis SETNX 做新節目快篩(第一線去重)。
type Deduper struct {
	client *redis.Client
}

// NewDeduper 建立連線並 ping 確認可用。
func NewDeduper(addr string) (*Deduper, error) {
	client := redis.NewClient(&redis.Options{Addr: addr})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return &Deduper{client: client}, nil
}

// Close 關閉 Redis 連線。
func (d *Deduper) Close() error { return d.client.Close() }

// IsNew 用 SETNX 判斷 (source, eventID) 是否第一次出現。
func (d *Deduper) IsNew(ctx context.Context, source, eventID string) (bool, error) {
	key := fmt.Sprintf("dedup:%s:%s", source, eventID)
	ok, err := d.client.SetNX(ctx, key, 1, dedupTTL).Result()
	if err != nil {
		return false, fmt.Errorf("redis setnx: %w", err)
	}
	return ok, nil
}
