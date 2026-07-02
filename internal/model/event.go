package model

import (
	"encoding/json"
	"time"
)

// Event 是所有資料源正規化後的統一節目型別。
type Event struct {
	ID            int64           `json:"id"`
	Source        string          `json:"source"`
	SourceEventID string          `json:"source_event_id"`
	Title         string          `json:"title"`
	URL           string          `json:"url"`
	Venue         string          `json:"venue,omitempty"`
	StartTime     *time.Time      `json:"start_time,omitempty"`
	OnSaleTime    *time.Time      `json:"on_sale_time,omitempty"`
	Raw           json.RawMessage `json:"-"`
	CreatedAt     time.Time       `json:"created_at"`
}
