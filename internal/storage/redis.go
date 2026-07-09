package storage

import (
	"context"
	"errors"
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

// IsNew 用 SET NX 判斷 (source, eventID) 是否第一次出現。
func (d *Deduper) IsNew(ctx context.Context, source, eventID string) (bool, error) {
	key := fmt.Sprintf("dedup:%s:%s", source, eventID)
	// SetArgs 的 NX 模式:key 已存在時回 redis.Nil,不算錯誤。
	_, err := d.client.SetArgs(ctx, key, 1, redis.SetArgs{Mode: "NX", TTL: dedupTTL}).Result()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("redis set nx: %w", err)
	}
	return true, nil
}

// Forget 移除 (source, eventID) 的去重標記;insert 失敗時讓下一輪能重試。
func (d *Deduper) Forget(ctx context.Context, source, eventID string) error {
	key := fmt.Sprintf("dedup:%s:%s", source, eventID)
	if err := d.client.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("redis del: %w", err)
	}
	return nil
}
