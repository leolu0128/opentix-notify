package storage

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDeduper_IsNew(t *testing.T) {
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR not set")
	}
	d, err := NewDeduper(addr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	ctx := context.Background()
	key := fmt.Sprintf("test-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = d.client.Del(context.Background(), "dedup:test:"+key).Err() })

	isNew, err := d.IsNew(ctx, "test", key)
	require.NoError(t, err)
	require.True(t, isNew, "first sighting should be new")
	require.Greater(t, d.client.TTL(ctx, "dedup:test:"+key).Val(), time.Duration(0))

	isNew, err = d.IsNew(ctx, "test", key)
	require.NoError(t, err)
	require.False(t, isNew, "second sighting should not be new")
}

func TestDeduper_ForgetAllowsRetry(t *testing.T) {
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR not set")
	}
	d, err := NewDeduper(addr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	ctx := context.Background()
	key := fmt.Sprintf("test-forget-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = d.client.Del(context.Background(), "dedup:test:"+key).Err() })

	isNew, err := d.IsNew(ctx, "test", key)
	require.NoError(t, err)
	require.True(t, isNew, "first sighting should be new")

	require.NoError(t, d.Forget(ctx, "test", key))

	isNew, err = d.IsNew(ctx, "test", key)
	require.NoError(t, err)
	require.True(t, isNew, "Forget 撤銷標記後,同一 key 應再次視為新事件")
}
