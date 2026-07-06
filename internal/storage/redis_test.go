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

	isNew, err := d.IsNew(ctx, "test", key)
	require.NoError(t, err)
	require.True(t, isNew, "first sighting should be new")

	isNew, err = d.IsNew(ctx, "test", key)
	require.NoError(t, err)
	require.False(t, isNew, "second sighting should not be new")
}
