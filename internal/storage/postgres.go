package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib"

	"gocrawler/internal/model"
)

// ErrNotFound 表示查無資料;API 層據此回 404。
var ErrNotFound = errors.New("not found")

// PostgresStore 是 events 表的存取層。
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore 開啟連線池並 ping 確認可用。
func NewPostgresStore(databaseURL string) (*PostgresStore, error) {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return &PostgresStore{db: db}, nil
}

func (s *PostgresStore) Close() error { return s.db.Close() }

// InsertEvent 寫入節目;(source, source_event_id) 已存在時不寫入並回傳 false。
// 回傳值 inserted 是「是否為新節目」的最終判準(去重最後防線)。
func (s *PostgresStore) InsertEvent(ctx context.Context, e model.Event) (inserted bool, err error) {
	var id int64
	err = s.db.QueryRowContext(ctx, `
		INSERT INTO events (source, source_event_id, title, url, venue, start_time, on_sale_time, raw)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (source, source_event_id) DO NOTHING
		RETURNING id`,
		e.Source, e.SourceEventID, e.Title, e.URL, e.Venue, e.StartTime, e.OnSaleTime, nullableRaw(e.Raw),
	).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil // conflict:已存在
	}
	if err != nil {
		return false, fmt.Errorf("insert event: %w", err)
	}
	return true, nil
}

// nullableRaw 空 slice 轉 nil,避免寫入空字串造成 JSONB 解析錯誤。
func nullableRaw(raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	return raw
}

// ListEvents 依標題關鍵字(ILIKE)與來源過濾,新的在前,分頁回傳。
func (s *PostgresStore) ListEvents(ctx context.Context, q, source string, limit, offset int) ([]model.Event, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, source, source_event_id, title, url, venue, start_time, on_sale_time, created_at
		FROM events
		WHERE ($1 = '' OR title ILIKE '%' || $1 || '%')
		  AND ($2 = '' OR source = $2)
		ORDER BY id DESC
		LIMIT $3 OFFSET $4`, q, source, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()

	var events []model.Event
	for rows.Next() {
		var e model.Event
		if err := rows.Scan(&e.ID, &e.Source, &e.SourceEventID, &e.Title, &e.URL,
			&e.Venue, &e.StartTime, &e.OnSaleTime, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate events: %w", err)
	}
	return events, nil
}

// GetEvent 以主鍵查詢單筆;查無回 ErrNotFound。
func (s *PostgresStore) GetEvent(ctx context.Context, id int64) (*model.Event, error) {
	var e model.Event
	err := s.db.QueryRowContext(ctx, `
		SELECT id, source, source_event_id, title, url, venue, start_time, on_sale_time, created_at
		FROM events WHERE id = $1`, id,
	).Scan(&e.ID, &e.Source, &e.SourceEventID, &e.Title, &e.URL,
		&e.Venue, &e.StartTime, &e.OnSaleTime, &e.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get event: %w", err)
	}
	return &e, nil
}
