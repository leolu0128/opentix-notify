package retry

import (
	"context"
	"time"
)

// Do 最多執行 fn attempts 次,失敗後以 baseDelay * 2^n 指數退避。
// context 取消時立即回傳 ctx.Err()。
func Do(ctx context.Context, attempts int, baseDelay time.Duration, fn func() error) error {
	var err error
	delay := baseDelay
	for i := 0; i < attempts; i++ {
		if err = fn(); err == nil {
			return nil
		}
		if i == attempts-1 {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		delay *= 2
	}
	return err
}
