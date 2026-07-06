// Package opentix 透過 OPENTIX 公開 search API 抓取節目列表。
package opentix

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"gocrawler/internal/model"
)

const (
	userAgent = "gocrawler/1.0 (polite hourly crawler)"
	sortBy    = "ABOUT_TO_BEGIN"
	language  = "zh-CHT"
	maxPages  = 40 // 40 頁 x 15 筆 = 600,現況全站音樂類約 488 筆
)

// Source 是 OPENTIX 的 scraper.Source 實作。
type Source struct {
	searchURL  string
	categories []string
	client     *http.Client
	pageDelay  time.Duration // 頁間停頓,測試時可設 0
}

// New 建立 OPENTIX 資料源。searchURL 為 search API 位址
// (如 https://search.opentix.life/search),categories 不可為空
// (API 對空的 categoryFilter 回 400)。
func New(searchURL string, categories []string) (*Source, error) {
	if len(categories) == 0 {
		return nil, fmt.Errorf("opentix: categories must not be empty")
	}
	return &Source{
		searchURL:  searchURL,
		categories: categories,
		client:     &http.Client{Timeout: 30 * time.Second},
		pageDelay:  500 * time.Millisecond,
	}, nil
}

func (s *Source) Name() string { return "opentix" }

type searchRequest struct {
	Language       string   `json:"language"`
	CategoryFilter []string `json:"categoryFilter"`
	SortBy         string   `json:"sortBy"`
	Offset         int      `json:"offset"`
}

type searchResponse struct {
	Result struct {
		HitsCount int `json:"hitsCount"`
		Found     []struct {
			Source json.RawMessage `json:"source"`
		} `json:"found"`
		NextOffset int `json:"nextOffset"`
	} `json:"result"`
}

type programItem struct {
	ID                  string `json:"id"`
	Title               string `json:"title"`
	StartDateTime       int64  `json:"startDateTime"`       // 毫秒
	OnlineStartDateTime int64  `json:"onlineStartDateTime"` // 毫秒,開賣時間
	EventVenues         []struct {
		City string `json:"city"`
		Name string `json:"name"`
	} `json:"eventVenues"`
	Places []string `json:"places"`
}

// Fetch 以 offset 分頁掃描全部節目(最多 maxPages 頁,頁間停頓 pageDelay),
// 每筆正規化為 model.Event。順序不影響正確性,去重由 pipeline 負責。
func (s *Source) Fetch(ctx context.Context) ([]model.Event, error) {
	var events []model.Event
	offset := 0
	for page := 0; page < maxPages; page++ {
		if page > 0 && s.pageDelay > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(s.pageDelay):
			}
		}
		resp, err := s.fetchPage(ctx, offset)
		if err != nil {
			return nil, err
		}
		if len(resp.Result.Found) == 0 {
			break
		}
		for _, f := range resp.Result.Found {
			e, err := toEvent(f.Source)
			if err != nil {
				return nil, err
			}
			events = append(events, e)
		}
		if resp.Result.NextOffset <= offset || resp.Result.NextOffset >= resp.Result.HitsCount {
			break
		}
		offset = resp.Result.NextOffset
	}
	return events, nil
}

func (s *Source) fetchPage(ctx context.Context, offset int) (*searchResponse, error) {
	body, err := json.Marshal(searchRequest{
		Language:       language,
		CategoryFilter: s.categories,
		SortBy:         sortBy,
		Offset:         offset,
	})
	if err != nil {
		return nil, fmt.Errorf("opentix: marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.searchURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("opentix: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("opentix: request offset %d: %w", offset, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("opentix: offset %d unexpected status %d", offset, resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("opentix: read offset %d: %w", offset, err)
	}
	var sr searchResponse
	if err := json.Unmarshal(data, &sr); err != nil {
		return nil, fmt.Errorf("opentix: parse offset %d: %w", offset, err)
	}
	return &sr, nil
}

func toEvent(raw json.RawMessage) (model.Event, error) {
	var item programItem
	if err := json.Unmarshal(raw, &item); err != nil {
		return model.Event{}, fmt.Errorf("opentix: parse program: %w", err)
	}
	if item.ID == "" {
		return model.Event{}, fmt.Errorf("opentix: program missing id")
	}
	e := model.Event{
		Source:        "opentix",
		SourceEventID: item.ID,
		Title:         item.Title,
		URL:           "https://www.opentix.life/event/" + item.ID,
		Venue:         venueOf(item),
		Raw:           raw,
	}
	if item.StartDateTime > 0 {
		ts := time.UnixMilli(item.StartDateTime)
		e.StartTime = &ts
	}
	if item.OnlineStartDateTime > 0 {
		ts := time.UnixMilli(item.OnlineStartDateTime)
		e.OnSaleTime = &ts
	}
	return e, nil
}

// venueOf 單一場館顯示「城市 場館名」,多場館以頓號連接城市清單。
func venueOf(item programItem) string {
	if len(item.EventVenues) == 1 {
		v := item.EventVenues[0]
		return strings.TrimSpace(v.City + " " + v.Name)
	}
	return strings.Join(item.Places, "、")
}
