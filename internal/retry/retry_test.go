package retry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDo_SucceedsFirstTry(t *testing.T) {
	calls := 0
	err := Do(context.Background(), 3, time.Millisecond, func() error {
		calls++
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, calls)
}

func TestDo_RetriesThenSucceeds(t *testing.T) {
	calls := 0
	err := Do(context.Background(), 3, time.Millisecond, func() error {
		calls++
		if calls < 3 {
			return errors.New("boom")
		}
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 3, calls)
}

func TestDo_ExhaustsAttempts(t *testing.T) {
	calls := 0
	err := Do(context.Background(), 3, time.Millisecond, func() error {
		calls++
		return errors.New("boom")
	})
	require.Error(t, err)
	require.Equal(t, 3, calls)
}

func TestDo_ContextCancelStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	err := Do(ctx, 3, time.Minute, func() error {
		calls++
		return errors.New("boom")
	})
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, calls) // 第一次失敗後等待時被 cancel
}
