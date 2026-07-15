// Package pipeline 串起 fetch→dedup→insert→match→notify 的一輪抓取流程。
package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"gocrawler/internal/matcher"
	"gocrawler/internal/model"
	"gocrawler/internal/retry"
)

// 消費端定義窄 interface,方便測試 mock;
// storage.PostgresStore / storage.Deduper / notifier 實作自動滿足。
type Source interface {
	Name() string
	Fetch(ctx context.Context) ([]model.Event, error)
}

type EventStore interface {
	InsertEvent(ctx context.Context, e model.Event) (bool, error)
}

type Deduper interface {
	IsNew(ctx context.Context, source, eventID string) (bool, error)
	Forget(ctx context.Context, source, eventID string) error
}

type Notifier interface {
	Notify(ctx context.Context, e model.Event) error
}

// Pipeline 的去重雙保險:Deduper(Redis)快篩失敗時降級,
// Store(Postgres UNIQUE)的 inserted 回傳值才是通知與否的最終判準。
type Pipeline struct {
	Sources  []Source
	Store    EventStore
	Deduper  Deduper
	Matcher  *matcher.Matcher
	Notifier Notifier
	Notify   bool // false = 只入庫不推播(初次 seed 用)
	// BaseDelay 是 retry 指數退避的基準間隔;零值時用預設 2s(測試可縮短)。
	BaseDelay time.Duration
}

const (
	fetchAttempts    = 3
	notifyAttempts   = 3
	defaultBaseDelay = 2 * time.Second
)

// baseDelay 回傳退避基準:有覆寫用覆寫,否則用預設。
func (p *Pipeline) baseDelay() time.Duration {
	if p.BaseDelay > 0 {
		return p.BaseDelay
	}
	return defaultBaseDelay
}

// Run 對所有 Source 執行一輪 fetch→dedup→insert→match→notify。
// 任一 source 失敗會記入回傳錯誤,但不影響其他 source;
// context 取消時立即停止並回傳 ctx.Err()。
func (p *Pipeline) Run(ctx context.Context) error {
	var firstErr error
	for _, src := range p.Sources {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := p.runSource(ctx, src); err != nil {
			slog.Error("source run failed", "source", src.Name(), "err", err)
			if firstErr == nil {
				firstErr = fmt.Errorf("source %s: %w", src.Name(), err)
			}
		}
	}
	return firstErr
}

func (p *Pipeline) runSource(ctx context.Context, src Source) error {
	start := time.Now()
	var events []model.Event
	err := retry.Do(ctx, fetchAttempts, p.baseDelay(), func() error {
		var ferr error
		events, ferr = src.Fetch(ctx)
		return ferr
	})
	if err != nil {
		return fmt.Errorf("fetch: %w", err)
	}
	slog.Info("fetched", "source", src.Name(), "count", len(events))

	var newCount, notifiedCount, insertErrs, notifyErrs int
	for _, e := range events {
		if err := ctx.Err(); err != nil {
			return err
		}

		// 第一線:Redis 快篩。Redis 故障時降級,交給 Postgres 判斷。
		isNew, derr := p.Deduper.IsNew(ctx, e.Source, e.SourceEventID)
		if derr != nil {
			// ctx 取消不是「deduper unavailable」,直接停止這輪。
			if ctx.Err() != nil {
				return ctx.Err()
			}
			slog.Warn("deduper unavailable, falling back to postgres",
				"source", e.Source, "event", e.SourceEventID, "err", derr)
		} else if !isNew {
			continue
		}

		// 最終防線:UNIQUE 衝突 = 不是新節目,不通知。
		inserted, ierr := p.Store.InsertEvent(ctx, e)
		if ierr != nil {
			insertErrs++
			slog.Error("insert failed", "source", e.Source, "event", e.SourceEventID, "err", ierr)
			// best-effort 撤銷 Redis 標記,讓下一輪能重試這筆;失敗只 log(90 天 TTL 是最終保險)。
			// ctx 可能已取消(這正是 insert 失敗的可能原因),用 detached ctx 確保撤銷能送達。
			fctx, fcancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			if ferr := p.Deduper.Forget(fctx, e.Source, e.SourceEventID); ferr != nil {
				slog.Warn("dedup forget failed", "source", e.Source, "event", e.SourceEventID, "err", ferr)
			}
			fcancel()
			continue
		}
		if !inserted {
			continue
		}
		newCount++
		slog.Info("new event stored", "source", e.Source, "title", e.Title)

		if !p.Notify || !p.Matcher.Match(e.Title) {
			continue
		}
		nerr := retry.Do(ctx, notifyAttempts, p.baseDelay(), func() error {
			return p.Notifier.Notify(ctx, e)
		})
		if nerr != nil {
			notifyErrs++
			// 節目已入庫不會遺失,漏一次通知只記 log。
			slog.Error("notify failed", "source", e.Source, "event", e.SourceEventID, "title", e.Title, "err", nerr)
		} else {
			notifiedCount++
		}
	}
	slog.Info("source round done", "source", src.Name(), "fetched", len(events),
		"new", newCount, "notified", notifiedCount,
		"insert_errors", insertErrs, "notify_errors", notifyErrs,
		"duration", time.Since(start).Round(time.Millisecond))
	return nil
}
