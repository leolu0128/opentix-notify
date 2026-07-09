package pipeline

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"gocrawler/internal/matcher"
	"gocrawler/internal/model"
)

type fakeSource struct {
	events []model.Event
	err    error
}

func (f *fakeSource) Name() string { return "fake" }
func (f *fakeSource) Fetch(ctx context.Context) ([]model.Event, error) {
	return f.events, f.err
}

type fakeStore struct {
	existing  map[string]bool
	inserted  []model.Event
	failFirst bool // true = 第一次 InsertEvent 回錯誤,之後恢復正常
	calls     int
}

func (f *fakeStore) InsertEvent(ctx context.Context, e model.Event) (bool, error) {
	f.calls++
	if f.failFirst && f.calls == 1 {
		return false, errors.New("db down")
	}
	key := e.Source + ":" + e.SourceEventID
	if f.existing[key] {
		return false, nil
	}
	f.existing[key] = true
	f.inserted = append(f.inserted, e)
	return true, nil
}

type fakeDeduper struct {
	seen      map[string]bool
	err       error
	forgotten []string
}

func (f *fakeDeduper) IsNew(ctx context.Context, source, id string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	key := source + ":" + id
	if f.seen[key] {
		return false, nil
	}
	f.seen[key] = true
	return true, nil
}

func (f *fakeDeduper) Forget(ctx context.Context, source, id string) error {
	key := source + ":" + id
	f.forgotten = append(f.forgotten, key)
	delete(f.seen, key)
	return nil
}

type fakeNotifier struct {
	notified []model.Event
	err      error
}

func (f *fakeNotifier) Notify(ctx context.Context, e model.Event) error {
	if f.err != nil {
		return f.err
	}
	f.notified = append(f.notified, e)
	return nil
}

func newPipeline(src *fakeSource, store *fakeStore, dedup *fakeDeduper, notif *fakeNotifier, keywords []string, notify bool) *Pipeline {
	return &Pipeline{
		Sources:   []Source{src},
		Store:     store,
		Deduper:   dedup,
		Matcher:   matcher.New(keywords),
		Notifier:  notif,
		Notify:    notify,
		BaseDelay: time.Millisecond, // 測試不等真實退避
	}
}

func TestRun_NewMatchingEventNotifies(t *testing.T) {
	src := &fakeSource{events: []model.Event{{Source: "fake", SourceEventID: "1", Title: "交響音樂會"}}}
	store := &fakeStore{existing: map[string]bool{}}
	dedup := &fakeDeduper{seen: map[string]bool{}}
	notif := &fakeNotifier{}

	p := newPipeline(src, store, dedup, notif, []string{"交響"}, true)
	require.NoError(t, p.Run(context.Background()))
	require.Len(t, store.inserted, 1)
	require.Len(t, notif.notified, 1)
}

func TestRun_NewNonMatchingEventStoredNotNotified(t *testing.T) {
	src := &fakeSource{events: []model.Event{{Source: "fake", SourceEventID: "1", Title: "歌劇之夜"}}}
	store := &fakeStore{existing: map[string]bool{}}
	dedup := &fakeDeduper{seen: map[string]bool{}}
	notif := &fakeNotifier{}

	p := newPipeline(src, store, dedup, notif, []string{"交響"}, true)
	require.NoError(t, p.Run(context.Background()))
	require.Len(t, store.inserted, 1, "非命中節目仍要入庫")
	require.Empty(t, notif.notified)
}

func TestRun_DuplicateInRedisSkipped(t *testing.T) {
	src := &fakeSource{events: []model.Event{{Source: "fake", SourceEventID: "1", Title: "交響音樂會"}}}
	store := &fakeStore{existing: map[string]bool{}}
	dedup := &fakeDeduper{seen: map[string]bool{"fake:1": true}}
	notif := &fakeNotifier{}

	p := newPipeline(src, store, dedup, notif, []string{"交響"}, true)
	require.NoError(t, p.Run(context.Background()))
	require.Empty(t, store.inserted, "Redis 已見過就不再走後續")
	require.Empty(t, notif.notified)
}

func TestRun_DuplicateInPostgresNotNotified(t *testing.T) {
	src := &fakeSource{events: []model.Event{{Source: "fake", SourceEventID: "1", Title: "交響音樂會"}}}
	store := &fakeStore{existing: map[string]bool{"fake:1": true}}
	dedup := &fakeDeduper{seen: map[string]bool{}} // Redis 資料掉了
	notif := &fakeNotifier{}

	p := newPipeline(src, store, dedup, notif, []string{"交響"}, true)
	require.NoError(t, p.Run(context.Background()))
	require.Empty(t, notif.notified, "Postgres UNIQUE 是最終防線")
}

func TestRun_RedisDownFallsBackToPostgres(t *testing.T) {
	src := &fakeSource{events: []model.Event{{Source: "fake", SourceEventID: "1", Title: "交響音樂會"}}}
	store := &fakeStore{existing: map[string]bool{}}
	dedup := &fakeDeduper{err: errors.New("redis down")}
	notif := &fakeNotifier{}

	p := newPipeline(src, store, dedup, notif, []string{"交響"}, true)
	require.NoError(t, p.Run(context.Background()))
	require.Len(t, store.inserted, 1, "Redis 掛掉時降級,仍完成入庫與通知")
	require.Len(t, notif.notified, 1)
}

func TestRun_NotifyDisabled(t *testing.T) {
	src := &fakeSource{events: []model.Event{{Source: "fake", SourceEventID: "1", Title: "交響音樂會"}}}
	store := &fakeStore{existing: map[string]bool{}}
	dedup := &fakeDeduper{seen: map[string]bool{}}
	notif := &fakeNotifier{}

	p := newPipeline(src, store, dedup, notif, []string{"交響"}, false)
	require.NoError(t, p.Run(context.Background()))
	require.Len(t, store.inserted, 1)
	require.Empty(t, notif.notified, "初次 seed 用 -no-notify 避免洗頻")
}

func TestRun_SourceErrorReturned(t *testing.T) {
	src := &fakeSource{err: errors.New("fetch failed")}
	store := &fakeStore{existing: map[string]bool{}}
	dedup := &fakeDeduper{seen: map[string]bool{}}
	notif := &fakeNotifier{}

	p := newPipeline(src, store, dedup, notif, []string{"交響"}, true)
	err := p.Run(context.Background())
	require.Error(t, err)
}

func TestRun_NotifyErrorDoesNotFailRun(t *testing.T) {
	src := &fakeSource{events: []model.Event{{Source: "fake", SourceEventID: "1", Title: "交響音樂會"}}}
	store := &fakeStore{existing: map[string]bool{}}
	dedup := &fakeDeduper{seen: map[string]bool{}}
	notif := &fakeNotifier{err: errors.New("webhook down")}

	p := newPipeline(src, store, dedup, notif, []string{"交響"}, true)
	require.NoError(t, p.Run(context.Background()), "節目已入庫,推播失敗只記 log 不算 run 失敗")
	require.Len(t, store.inserted, 1)
}

func TestRun_InsertFailureRevertsDedupMark(t *testing.T) {
	src := &fakeSource{events: []model.Event{
		{Source: "fake", SourceEventID: "1", Title: "交響音樂會"},
		{Source: "fake", SourceEventID: "2", Title: "交響第九"},
	}}
	store := &fakeStore{existing: map[string]bool{}, failFirst: true}
	dedup := &fakeDeduper{seen: map[string]bool{}}
	notif := &fakeNotifier{}

	p := newPipeline(src, store, dedup, notif, []string{"交響"}, true)
	require.NoError(t, p.Run(context.Background()), "單筆 insert 失敗只記 log,不算 run 失敗")
	require.Equal(t, []string{"fake:1"}, dedup.forgotten,
		"insert 失敗要撤銷 Redis 標記,下一輪才有機會重試這筆")
	require.Len(t, store.inserted, 1, "後續事件照常處理")
	require.Equal(t, "2", store.inserted[0].SourceEventID)
	require.Len(t, notif.notified, 1)
}

func TestRun_ContextCancelledStopsRun(t *testing.T) {
	src := &fakeSource{events: []model.Event{{Source: "fake", SourceEventID: "1", Title: "交響音樂會"}}}
	store := &fakeStore{existing: map[string]bool{}}
	dedup := &fakeDeduper{seen: map[string]bool{}}
	notif := &fakeNotifier{}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	p := newPipeline(src, store, dedup, notif, []string{"交響"}, true)
	err := p.Run(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, store.inserted, "取消後不應再打 store")
	require.Empty(t, notif.notified, "取消後不應再打 notifier")
}

func TestRun_SourceFailureDoesNotBlockOthers(t *testing.T) {
	bad := &fakeSource{err: errors.New("fetch failed")}
	good := &fakeSource{events: []model.Event{{Source: "fake", SourceEventID: "2", Title: "交響音樂會"}}}
	store := &fakeStore{existing: map[string]bool{}}
	dedup := &fakeDeduper{seen: map[string]bool{}}
	notif := &fakeNotifier{}

	p := &Pipeline{
		Sources:   []Source{bad, good},
		Store:     store,
		Deduper:   dedup,
		Matcher:   matcher.New([]string{"交響"}),
		Notifier:  notif,
		Notify:    true,
		BaseDelay: time.Millisecond,
	}
	err := p.Run(context.Background())
	require.Error(t, err, "第一個 source 失敗要反映在回傳錯誤")
	require.Len(t, store.inserted, 1, "其他 source 不受影響,照常入庫")
	require.Len(t, notif.notified, 1)
}
