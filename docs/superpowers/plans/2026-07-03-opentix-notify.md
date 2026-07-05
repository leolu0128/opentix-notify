# OPENTIX 新節目上架通知 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 定時抓取 OPENTIX 節目列表,新節目去重入庫,符合關鍵字即推播 Discord Webhook,並提供查詢 API。

**Architecture:** 單一 binary(`serve` = Gin API + cron 排程;`scrape` = 手動單次抓取),`internal/` 分層:scraper(Source interface + OPENTIX 實作)→ pipeline(去重 Redis SETNX + Postgres UNIQUE 雙保險)→ matcher(關鍵字)→ notifier(Discord Webhook)。

**Tech Stack:** Go 1.22+、Gin、pgx(database/sql driver)、go-redis v9、robfig/cron v3、golang-migrate(library + embed)、yaml.v3、testify、Docker Compose(Postgres 16 + Redis 7)、GitHub Actions。

**Module 名稱:** `gocrawler`(純執行檔專案,不會被外部 import,用短名讓 import path 乾淨且不綁 GitHub 帳號)。

**設計文件:** `docs/superpowers/specs/2026-07-03-opentix-notify-design.md`

---

## 檔案結構總覽

```
gocrawler/
├── cmd/app/main.go                    # serve | scrape 子指令
├── internal/
│   ├── config/config.go               # YAML + env 覆蓋
│   ├── model/event.go                 # Event struct(共用資料型別)
│   ├── matcher/matcher.go             # 關鍵字比對
│   ├── retry/retry.go                 # 指數退避重試
│   ├── scraper/
│   │   ├── source.go                  # Source interface
│   │   └── opentix/opentix.go         # OPENTIX JSON API 實作
│   │   └── opentix/testdata/list.json # 真實回應 fixture
│   ├── notifier/
│   │   ├── notifier.go                # Notifier interface
│   │   └── discord/discord.go         # Discord Webhook 實作
│   ├── storage/
│   │   ├── postgres.go                # EventStore(insert/list/get)
│   │   ├── redis.go                   # Deduper(SETNX)
│   │   └── migrate.go                 # embed migrations + 啟動時執行
│   ├── pipeline/pipeline.go           # fetch→dedup→insert→match→notify
│   └── api/
│       ├── router.go                  # Gin routes
│       └── handlers.go                # /events, /events/:id, /healthz
├── migrations/
│   ├── 0001_create_events.up.sql
│   └── 0001_create_events.down.sql
├── config.example.yaml
├── docker-compose.yml
├── Dockerfile
└── .github/workflows/ci.yml
```

---

### Task 1: Go module、目錄骨架與 CI

**Files:**
- Create: `go.mod`(via `go mod init`)
- Create: `.github/workflows/ci.yml`
- Create: `config.example.yaml`

- [ ] **Step 1: 初始化 module**

Run: `go mod init gocrawler`
Expected: 建立 `go.mod`,內容含 `module gocrawler`

- [ ] **Step 2: 建立 CI workflow**

`.github/workflows/ci.yml`:

```yaml
name: CI
on:
  push:
    branches: [main]
  pull_request:

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: "1.22"
      - name: Test
        run: go test ./...
      - name: Lint
        uses: golangci/golangci-lint-action@v6
        with:
          version: latest
```

- [ ] **Step 3: 建立設定範例檔**

`config.example.yaml`:

```yaml
# 複製為 config.yaml 後填入實際值;secrets 建議改用環境變數
keywords:
  - 交響
  - 鋼琴
cron: "0 * * * *"          # 每小時整點
opentix_url: "https://www.opentix.life"   # Task 7 驗證實際 API endpoint 後更新
database_url: "postgres://gocrawler:gocrawler@localhost:5432/gocrawler?sslmode=disable"
redis_addr: "localhost:6379"
discord_webhook_url: ""     # 一律用環境變數 DISCORD_WEBHOOK_URL 提供,不要寫進檔案
```

- [ ] **Step 4: 把 config.yaml 加入 .gitignore**

在 `.gitignore` 末尾追加:

```
# 本地設定(含機密)
config.yaml
```

- [ ] **Step 5: Commit**

```bash
git add go.mod .github/ config.example.yaml .gitignore
git commit -m "chore: init go module, CI workflow, config example"
```

---

### Task 2: Event model

**Files:**
- Create: `internal/model/event.go`

- [ ] **Step 1: 建立 Event struct(純型別,無邏輯,不需測試)**

`internal/model/event.go`:

```go
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
```

- [ ] **Step 2: 確認編譯通過**

Run: `go build ./...`
Expected: 無錯誤

- [ ] **Step 3: Commit**

```bash
git add internal/model/
git commit -m "feat: add Event model"
```

---

### Task 3: Config 載入(YAML + env 覆蓋)

**Files:**
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

- [ ] **Step 1: 寫失敗測試**

`internal/config/config_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestLoad_FromYAML(t *testing.T) {
	path := writeTempConfig(t, `
keywords: [交響, 鋼琴]
cron: "0 * * * *"
opentix_url: "https://example.com/api"
database_url: "postgres://u:p@localhost:5432/db"
redis_addr: "localhost:6379"
discord_webhook_url: "https://discord.com/api/webhooks/x"
`)
	cfg, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, []string{"交響", "鋼琴"}, cfg.Keywords)
	require.Equal(t, "0 * * * *", cfg.Cron)
	require.Equal(t, "https://example.com/api", cfg.OpentixURL)
}

func TestLoad_EnvOverridesYAML(t *testing.T) {
	path := writeTempConfig(t, `
database_url: "postgres://from-yaml"
discord_webhook_url: "https://from-yaml"
`)
	t.Setenv("DATABASE_URL", "postgres://from-env")
	t.Setenv("DISCORD_WEBHOOK_URL", "https://from-env")
	t.Setenv("REDIS_ADDR", "redis-env:6379")

	cfg, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, "postgres://from-env", cfg.DatabaseURL)
	require.Equal(t, "https://from-env", cfg.DiscordWebhookURL)
	require.Equal(t, "redis-env:6379", cfg.RedisAddr)
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := Load("does-not-exist.yaml")
	require.Error(t, err)
}
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `go get github.com/stretchr/testify && go test ./internal/config/ -v`
Expected: FAIL,`Load` 未定義(compile error)

- [ ] **Step 3: 實作**

`internal/config/config.go`:

```go
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Keywords          []string `yaml:"keywords"`
	Cron              string   `yaml:"cron"`
	OpentixURL        string   `yaml:"opentix_url"`
	DatabaseURL       string   `yaml:"database_url"`
	RedisAddr         string   `yaml:"redis_addr"`
	DiscordWebhookURL string   `yaml:"discord_webhook_url"`
}

// Load 讀取 YAML 設定檔,環境變數(DATABASE_URL、REDIS_ADDR、
// DISCORD_WEBHOOK_URL)存在時覆蓋對應欄位。
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if v := os.Getenv("DATABASE_URL"); v != "" {
		cfg.DatabaseURL = v
	}
	if v := os.Getenv("REDIS_ADDR"); v != "" {
		cfg.RedisAddr = v
	}
	if v := os.Getenv("DISCORD_WEBHOOK_URL"); v != "" {
		cfg.DiscordWebhookURL = v
	}
	return &cfg, nil
}
```

- [ ] **Step 4: 跑測試確認通過**

Run: `go get gopkg.in/yaml.v3 && go test ./internal/config/ -v`
Expected: 3 個測試 PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config/ go.mod go.sum
git commit -m "feat: config loading with env override"
```

---

### Task 4: Matcher(關鍵字比對)

**Files:**
- Create: `internal/matcher/matcher.go`
- Test: `internal/matcher/matcher_test.go`

規則:標題「不分大小寫包含任一關鍵字」即命中;**關鍵字清單為空時一律不命中**(避免第一次設定就洗頻)。

- [ ] **Step 1: 寫失敗測試**

`internal/matcher/matcher_test.go`:

```go
package matcher

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMatch(t *testing.T) {
	tests := []struct {
		name     string
		keywords []string
		title    string
		want     bool
	}{
		{"命中中文關鍵字", []string{"交響", "鋼琴"}, "貝多芬交響曲之夜", true},
		{"不分大小寫", []string{"nso"}, "NSO 開季音樂會", true},
		{"未命中", []string{"歌劇"}, "鋼琴獨奏會", false},
		{"空關鍵字清單不命中", []string{}, "任何節目", false},
		{"nil 關鍵字清單不命中", nil, "任何節目", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New(tt.keywords)
			require.Equal(t, tt.want, m.Match(tt.title))
		})
	}
}
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `go test ./internal/matcher/ -v`
Expected: FAIL,`New` 未定義

- [ ] **Step 3: 實作**

`internal/matcher/matcher.go`:

```go
package matcher

import "strings"

type Matcher struct {
	keywords []string // 已轉小寫
}

// New 建立關鍵字比對器。keywords 為空時 Match 一律回傳 false。
func New(keywords []string) *Matcher {
	lowered := make([]string, 0, len(keywords))
	for _, k := range keywords {
		lowered = append(lowered, strings.ToLower(k))
	}
	return &Matcher{keywords: lowered}
}

func (m *Matcher) Match(title string) bool {
	t := strings.ToLower(title)
	for _, k := range m.keywords {
		if strings.Contains(t, k) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: 跑測試確認通過**

Run: `go test ./internal/matcher/ -v`
Expected: 全部 PASS

- [ ] **Step 5: Commit**

```bash
git add internal/matcher/
git commit -m "feat: keyword matcher"
```

---

### Task 5: Retry helper(指數退避)

**Files:**
- Create: `internal/retry/retry.go`
- Test: `internal/retry/retry_test.go`

- [ ] **Step 1: 寫失敗測試**

`internal/retry/retry_test.go`:

```go
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
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `go test ./internal/retry/ -v`
Expected: FAIL,`Do` 未定義

- [ ] **Step 3: 實作**

`internal/retry/retry.go`:

```go
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
```

- [ ] **Step 4: 跑測試確認通過**

Run: `go test ./internal/retry/ -v`
Expected: 4 個測試 PASS

- [ ] **Step 5: Commit**

```bash
git add internal/retry/
git commit -m "feat: retry helper with exponential backoff"
```

---

### Task 6: Docker Compose、migration 與啟動時自動 migrate

**Files:**
- Create: `docker-compose.yml`
- Create: `migrations/0001_create_events.up.sql`
- Create: `migrations/0001_create_events.down.sql`
- Create: `internal/storage/migrate.go`

- [ ] **Step 1: docker-compose**

`docker-compose.yml`:

```yaml
services:
  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: gocrawler
      POSTGRES_PASSWORD: gocrawler
      POSTGRES_DB: gocrawler
    ports:
      - "5432:5432"
    volumes:
      - pgdata:/var/lib/postgresql/data
  redis:
    image: redis:7-alpine
    ports:
      - "6379:6379"

volumes:
  pgdata:
```

- [ ] **Step 2: migration SQL**

`migrations/0001_create_events.up.sql`:

```sql
CREATE TABLE events (
    id              BIGSERIAL PRIMARY KEY,
    source          TEXT        NOT NULL,
    source_event_id TEXT        NOT NULL,
    title           TEXT        NOT NULL,
    url             TEXT        NOT NULL,
    venue           TEXT        NOT NULL DEFAULT '',
    start_time      TIMESTAMPTZ,
    on_sale_time    TIMESTAMPTZ,
    raw             JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (source, source_event_id)
);
```

`migrations/0001_create_events.down.sql`:

```sql
DROP TABLE events;
```

- [ ] **Step 3: 啟動時自動 migrate(embed)**

`internal/storage/migrate.go`:

```go
package storage

import (
	"embed"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed 由 cmd 傳入,migrations 目錄在 repo 根,embed 需在根層級的 package 宣告。
// 因此這裡接收 fs 參數,embed.FS 由 main.go 持有(見 Task 12)。

// Migrate 對 databaseURL 套用 migrationsFS 內 migrations/ 目錄下的所有版本。
func Migrate(migrationsFS embed.FS, databaseURL string) error {
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("load migrations: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, databaseURL)
	if err != nil {
		return fmt.Errorf("init migrate: %w", err)
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: 安裝依賴並確認編譯**

Run: `go get github.com/golang-migrate/migrate/v4 github.com/golang-migrate/migrate/v4/database/postgres github.com/golang-migrate/migrate/v4/source/iofs && go build ./...`
Expected: 編譯通過

- [ ] **Step 5: 手動驗證 migration 可套用**

Run:
```bash
docker compose up -d postgres redis
```
(migrate 的實際執行在 Task 12 的 main.go 接上後驗證;此處先確認容器起得來)
Expected: `docker compose ps` 顯示兩個服務 running

- [ ] **Step 6: Commit**

```bash
git add docker-compose.yml migrations/ internal/storage/migrate.go go.mod go.sum
git commit -m "feat: docker-compose, events migration, auto-migrate on startup"
```

---

### Task 7: OPENTIX Source(先驗證真實 API,再寫 parser)

**Files:**
- Create: `internal/scraper/source.go`
- Create: `internal/scraper/opentix/opentix.go`
- Create: `internal/scraper/opentix/testdata/list.json`
- Test: `internal/scraper/opentix/opentix_test.go`

- [ ] **Step 1: 驗證 OPENTIX 實際 API(人工步驟)**

1. 瀏覽器開 `https://www.opentix.life` 的節目探索/搜尋頁
2. DevTools → Network → Fetch/XHR,找到回傳節目列表的請求(通常是 POST/GET 到 `api.opentix.life` 或同域 `/api/...` 路徑)
3. 記下:完整 URL、HTTP method、必要的 query/body 參數、回應 JSON 結構
4. 把一份**真實回應**存成 `internal/scraper/opentix/testdata/list.json`
5. **若真實結構與下方 Step 2 假設的結構不同**:以真實結構為準,同步修改 Step 2 的 fixture 假設、Step 4 的 `listResponse` struct 與欄位對映,再繼續。這是本計畫唯一允許偏離的地方,偏離時在 commit message 註明實際 endpoint 結構。

- [ ] **Step 2: fixture(假設結構,依 Step 1 實測修正)**

`internal/scraper/opentix/testdata/list.json`(範例假設;必須以 Step 1 抓到的真實回應覆蓋):

```json
{
  "data": {
    "list": [
      {
        "id": "OPX001",
        "title": "貝多芬交響曲之夜",
        "venue": "國家音樂廳",
        "startTime": "2026-08-01T19:30:00+08:00"
      },
      {
        "id": "OPX002",
        "title": "現代舞《光》",
        "venue": "國家戲劇院",
        "startTime": "2026-08-15T14:30:00+08:00"
      }
    ]
  }
}
```

- [ ] **Step 3: Source interface + 失敗測試**

`internal/scraper/source.go`:

```go
package scraper

import (
	"context"

	"gocrawler/internal/model"
)

// Source 是單一資料源的抽象。新平台(如 tixCraft)實作此 interface 即可接入。
type Source interface {
	Name() string
	Fetch(ctx context.Context) ([]model.Event, error)
}
```

`internal/scraper/opentix/opentix_test.go`:

```go
package opentix

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFetch_ParsesFixture(t *testing.T) {
	fixture, err := os.ReadFile("testdata/list.json")
	require.NoError(t, err)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	}))
	defer srv.Close()

	s := New(srv.URL)
	events, err := s.Fetch(context.Background())
	require.NoError(t, err)
	require.Len(t, events, 2)

	e := events[0]
	require.Equal(t, "opentix", e.Source)
	require.Equal(t, "OPX001", e.SourceEventID)
	require.Equal(t, "貝多芬交響曲之夜", e.Title)
	require.Equal(t, "國家音樂廳", e.Venue)
	require.Equal(t, "https://www.opentix.life/event/OPX001", e.URL)
	require.NotNil(t, e.StartTime)
	require.NotEmpty(t, e.Raw)
}

func TestFetch_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	s := New(srv.URL)
	_, err := s.Fetch(context.Background())
	require.Error(t, err)
}

func TestFetch_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	s := New(srv.URL)
	_, err := s.Fetch(context.Background())
	require.Error(t, err)
}
```

- [ ] **Step 4: 跑測試確認失敗**

Run: `go test ./internal/scraper/... -v`
Expected: FAIL,`New` 未定義

- [ ] **Step 5: 實作**

`internal/scraper/opentix/opentix.go`(欄位對映依 Step 1 實測調整):

```go
package opentix

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"gocrawler/internal/model"
)

const userAgent = "gocrawler/1.0 (+https://github.com/your-account/gocrawler; polite hourly crawler)"

type Source struct {
	baseURL string
	client  *http.Client
}

func New(baseURL string) *Source {
	return &Source{
		baseURL: baseURL,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

func (s *Source) Name() string { return "opentix" }

type listItem struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Venue     string `json:"venue"`
	StartTime string `json:"startTime"`
}

type listResponse struct {
	Data struct {
		List []listItem `json:"list"`
	} `json:"data"`
}

func (s *Source) Fetch(ctx context.Context) ([]model.Event, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL, nil)
	if err != nil {
		return nil, fmt.Errorf("opentix: build request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("opentix: request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("opentix: unexpected status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("opentix: read body: %w", err)
	}

	var lr listResponse
	if err := json.Unmarshal(body, &lr); err != nil {
		return nil, fmt.Errorf("opentix: parse response: %w", err)
	}

	events := make([]model.Event, 0, len(lr.Data.List))
	for _, item := range lr.Data.List {
		raw, _ := json.Marshal(item)
		e := model.Event{
			Source:        "opentix",
			SourceEventID: item.ID,
			Title:         item.Title,
			URL:           "https://www.opentix.life/event/" + item.ID,
			Venue:         item.Venue,
			Raw:           raw,
		}
		if ts, err := time.Parse(time.RFC3339, item.StartTime); err == nil {
			e.StartTime = &ts
		}
		events = append(events, e)
	}
	return events, nil
}
```

- [ ] **Step 6: 跑測試確認通過**

Run: `go test ./internal/scraper/... -v`
Expected: 3 個測試 PASS

- [ ] **Step 7: Commit**

```bash
git add internal/scraper/
git commit -m "feat: opentix source with fixture-based parser tests"
```

---

### Task 8: Storage — Postgres EventStore

**Files:**
- Create: `internal/storage/postgres.go`
- Test: `internal/storage/postgres_test.go`(整合測試,無 `TEST_DATABASE_URL` 時 skip)

- [ ] **Step 1: 寫失敗測試(整合測試)**

`internal/storage/postgres_test.go`:

```go
package storage

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"gocrawler/internal/model"
)

// 需要真實 Postgres:docker compose up -d postgres 後
// TEST_DATABASE_URL="postgres://gocrawler:gocrawler@localhost:5432/gocrawler?sslmode=disable" go test ./internal/storage/
func newTestStore(t *testing.T) *PostgresStore {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	store, err := NewPostgresStore(url)
	require.NoError(t, err)
	_, err = store.db.Exec(`CREATE TABLE IF NOT EXISTS events (
		id BIGSERIAL PRIMARY KEY, source TEXT NOT NULL, source_event_id TEXT NOT NULL,
		title TEXT NOT NULL, url TEXT NOT NULL, venue TEXT NOT NULL DEFAULT '',
		start_time TIMESTAMPTZ, on_sale_time TIMESTAMPTZ, raw JSONB,
		created_at TIMESTAMPTZ NOT NULL DEFAULT now(), UNIQUE (source, source_event_id))`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = store.db.Exec(`DELETE FROM events WHERE source = 'test'`)
		store.Close()
	})
	return store
}

func TestInsertEvent_NewAndDuplicate(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	e := model.Event{Source: "test", SourceEventID: "e1", Title: "節目一", URL: "https://x/1"}

	inserted, err := store.InsertEvent(ctx, e)
	require.NoError(t, err)
	require.True(t, inserted, "first insert should report inserted")

	inserted, err = store.InsertEvent(ctx, e)
	require.NoError(t, err)
	require.False(t, inserted, "duplicate insert should report not inserted")
}

func TestListEvents_FilterAndPagination(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	for _, e := range []model.Event{
		{Source: "test", SourceEventID: "l1", Title: "交響音樂會", URL: "https://x/1"},
		{Source: "test", SourceEventID: "l2", Title: "鋼琴獨奏", URL: "https://x/2"},
		{Source: "test", SourceEventID: "l3", Title: "交響傑作選", URL: "https://x/3"},
	} {
		_, err := store.InsertEvent(ctx, e)
		require.NoError(t, err)
	}

	got, err := store.ListEvents(ctx, "交響", "test", 10, 0)
	require.NoError(t, err)
	require.Len(t, got, 2)

	got, err = store.ListEvents(ctx, "", "test", 2, 0)
	require.NoError(t, err)
	require.Len(t, got, 2)
}

func TestGetEvent(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	_, err := store.InsertEvent(ctx, model.Event{Source: "test", SourceEventID: "g1", Title: "查詢測試", URL: "https://x/g1"})
	require.NoError(t, err)

	list, err := store.ListEvents(ctx, "查詢測試", "test", 1, 0)
	require.NoError(t, err)
	require.Len(t, list, 1)

	got, err := store.GetEvent(ctx, list[0].ID)
	require.NoError(t, err)
	require.Equal(t, "查詢測試", got.Title)

	_, err = store.GetEvent(ctx, -1)
	require.ErrorIs(t, err, ErrNotFound)
}
```

- [ ] **Step 2: 跑測試確認失敗**

Run:
```bash
docker compose up -d postgres
$env:TEST_DATABASE_URL = "postgres://gocrawler:gocrawler@localhost:5432/gocrawler?sslmode=disable"
go test ./internal/storage/ -v
```
Expected: FAIL,`NewPostgresStore` 未定義

- [ ] **Step 3: 實作**

`internal/storage/postgres.go`:

```go
package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib"

	"gocrawler/internal/model"
)

var ErrNotFound = errors.New("not found")

type PostgresStore struct {
	db *sql.DB
}

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

func nullableRaw(raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	return raw
}

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
	return events, rows.Err()
}

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
```

- [ ] **Step 4: 跑測試確認通過**

Run: `go get github.com/jackc/pgx/v5 && go test ./internal/storage/ -v`(同樣帶 `TEST_DATABASE_URL`)
Expected: 3 個測試 PASS

- [ ] **Step 5: Commit**

```bash
git add internal/storage/postgres.go internal/storage/postgres_test.go go.mod go.sum
git commit -m "feat: postgres event store with conflict-aware insert"
```

---

### Task 9: Storage — Redis Deduper

**Files:**
- Create: `internal/storage/redis.go`
- Test: `internal/storage/redis_test.go`(整合測試,無 `TEST_REDIS_ADDR` 時 skip)

- [ ] **Step 1: 寫失敗測試**

`internal/storage/redis_test.go`:

```go
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
	t.Cleanup(func() { d.Close() })

	ctx := context.Background()
	key := fmt.Sprintf("test-%d", time.Now().UnixNano())

	isNew, err := d.IsNew(ctx, "test", key)
	require.NoError(t, err)
	require.True(t, isNew, "first sighting should be new")

	isNew, err = d.IsNew(ctx, "test", key)
	require.NoError(t, err)
	require.False(t, isNew, "second sighting should not be new")
}
```

- [ ] **Step 2: 跑測試確認失敗**

Run:
```bash
docker compose up -d redis
$env:TEST_REDIS_ADDR = "localhost:6379"
go test ./internal/storage/ -run TestDeduper -v
```
Expected: FAIL,`NewDeduper` 未定義

- [ ] **Step 3: 實作**

`internal/storage/redis.go`:

```go
package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// dedupTTL 限制 Redis 記憶體用量;過期後 Postgres UNIQUE 仍擋住重複通知。
const dedupTTL = 90 * 24 * time.Hour

type Deduper struct {
	client *redis.Client
}

func NewDeduper(addr string) (*Deduper, error) {
	client := redis.NewClient(&redis.Options{Addr: addr})
	if err := client.Ping(context.Background()).Err(); err != nil {
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return &Deduper{client: client}, nil
}

func (d *Deduper) Close() error { return d.client.Close() }

// IsNew 用 SETNX 判斷 (source, eventID) 是否第一次出現。
func (d *Deduper) IsNew(ctx context.Context, source, eventID string) (bool, error) {
	key := fmt.Sprintf("dedup:%s:%s", source, eventID)
	ok, err := d.client.SetNX(ctx, key, 1, dedupTTL).Result()
	if err != nil {
		return false, fmt.Errorf("redis setnx: %w", err)
	}
	return ok, nil
}
```

- [ ] **Step 4: 跑測試確認通過**

Run: `go get github.com/redis/go-redis/v9 && go test ./internal/storage/ -run TestDeduper -v`(帶 `TEST_REDIS_ADDR`)
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/storage/redis.go internal/storage/redis_test.go go.mod go.sum
git commit -m "feat: redis deduper with SETNX and TTL"
```

---

### Task 10: Notifier — Discord Webhook

**Files:**
- Create: `internal/notifier/notifier.go`
- Create: `internal/notifier/discord/discord.go`
- Test: `internal/notifier/discord/discord_test.go`

- [ ] **Step 1: interface + 失敗測試**

`internal/notifier/notifier.go`:

```go
package notifier

import (
	"context"

	"gocrawler/internal/model"
)

// Notifier 是通知管道的抽象。二階段換 Discord Bot 時實作此 interface 即可。
type Notifier interface {
	Notify(ctx context.Context, e model.Event) error
}
```

`internal/notifier/discord/discord_test.go`:

```go
package discord

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"gocrawler/internal/model"
)

func TestNotify_SendsContent(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	start := time.Date(2026, 8, 1, 19, 30, 0, 0, time.FixedZone("CST", 8*3600))
	n := New(srv.URL)
	err := n.Notify(context.Background(), model.Event{
		Title: "貝多芬交響曲之夜", Venue: "國家音樂廳",
		URL: "https://www.opentix.life/event/OPX001", StartTime: &start,
	})
	require.NoError(t, err)

	var payload struct {
		Content string `json:"content"`
	}
	require.NoError(t, json.Unmarshal(gotBody, &payload))
	require.Contains(t, payload.Content, "貝多芬交響曲之夜")
	require.Contains(t, payload.Content, "國家音樂廳")
	require.Contains(t, payload.Content, "https://www.opentix.life/event/OPX001")
}

func TestNotify_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	n := New(srv.URL)
	err := n.Notify(context.Background(), model.Event{Title: "x", URL: "https://x"})
	require.Error(t, err)
}
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `go test ./internal/notifier/... -v`
Expected: FAIL,`New` 未定義

- [ ] **Step 3: 實作**

`internal/notifier/discord/discord.go`:

```go
package discord

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"gocrawler/internal/model"
)

type Webhook struct {
	url    string
	client *http.Client
}

func New(webhookURL string) *Webhook {
	return &Webhook{
		url:    webhookURL,
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

func (w *Webhook) Notify(ctx context.Context, e model.Event) error {
	content := fmt.Sprintf("🎫 新節目上架:%s", e.Title)
	if e.Venue != "" {
		content += fmt.Sprintf("\n📍 %s", e.Venue)
	}
	if e.StartTime != nil {
		content += fmt.Sprintf("\n🗓 %s", e.StartTime.Format("2006-01-02 15:04"))
	}
	content += "\n" + e.URL

	body, err := json.Marshal(map[string]string{"content": content})
	if err != nil {
		return fmt.Errorf("discord: marshal payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("discord: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("discord: request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("discord: unexpected status %d", resp.StatusCode)
	}
	return nil
}
```

- [ ] **Step 4: 跑測試確認通過**

Run: `go test ./internal/notifier/... -v`
Expected: 2 個測試 PASS

- [ ] **Step 5: Commit**

```bash
git add internal/notifier/
git commit -m "feat: discord webhook notifier"
```

---

### Task 11: Pipeline(核心編排)

**Files:**
- Create: `internal/pipeline/pipeline.go`
- Test: `internal/pipeline/pipeline_test.go`

去重語意:Redis `IsNew` 快篩 → Postgres `InsertEvent` 的 `inserted` 才是**通知與否的最終判準**。Redis 失敗(如連不上)時降級直接走 Postgres,不中斷 pipeline。

- [ ] **Step 1: 寫失敗測試(全 mock)**

`internal/pipeline/pipeline_test.go`:

```go
package pipeline

import (
	"context"
	"errors"
	"testing"

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
	existing map[string]bool
	inserted []model.Event
}

func (f *fakeStore) InsertEvent(ctx context.Context, e model.Event) (bool, error) {
	key := e.Source + ":" + e.SourceEventID
	if f.existing[key] {
		return false, nil
	}
	f.existing[key] = true
	f.inserted = append(f.inserted, e)
	return true, nil
}

type fakeDeduper struct {
	seen map[string]bool
	err  error
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
		Sources:  []Source{src},
		Store:    store,
		Deduper:  dedup,
		Matcher:  matcher.New(keywords),
		Notifier: notif,
		Notify:   notify,
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
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `go test ./internal/pipeline/ -v`
Expected: FAIL,`Pipeline` 未定義

- [ ] **Step 3: 實作**

`internal/pipeline/pipeline.go`:

```go
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
}

type Notifier interface {
	Notify(ctx context.Context, e model.Event) error
}

type Pipeline struct {
	Sources  []Source
	Store    EventStore
	Deduper  Deduper
	Matcher  *matcher.Matcher
	Notifier Notifier
	Notify   bool // false = 只入庫不推播(初次 seed 用)
}

const (
	fetchAttempts  = 3
	notifyAttempts = 3
	baseDelay      = 2 * time.Second
)

// Run 對所有 Source 執行一輪 fetch→dedup→insert→match→notify。
// 任一 source 失敗會記入回傳錯誤,但不影響其他 source。
func (p *Pipeline) Run(ctx context.Context) error {
	var firstErr error
	for _, src := range p.Sources {
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
	var events []model.Event
	err := retry.Do(ctx, fetchAttempts, baseDelay, func() error {
		var ferr error
		events, ferr = src.Fetch(ctx)
		return ferr
	})
	if err != nil {
		return fmt.Errorf("fetch: %w", err)
	}
	slog.Info("fetched", "source", src.Name(), "count", len(events))

	for _, e := range events {
		// 第一線:Redis 快篩。Redis 故障時降級,交給 Postgres 判斷。
		isNew, derr := p.Deduper.IsNew(ctx, e.Source, e.SourceEventID)
		if derr != nil {
			slog.Warn("deduper unavailable, falling back to postgres", "err", derr)
		} else if !isNew {
			continue
		}

		// 最終防線:UNIQUE 衝突 = 不是新節目,不通知。
		inserted, ierr := p.Store.InsertEvent(ctx, e)
		if ierr != nil {
			slog.Error("insert failed", "event", e.SourceEventID, "err", ierr)
			continue
		}
		if !inserted {
			continue
		}
		slog.Info("new event stored", "source", e.Source, "title", e.Title)

		if !p.Notify || !p.Matcher.Match(e.Title) {
			continue
		}
		nerr := retry.Do(ctx, notifyAttempts, baseDelay, func() error {
			return p.Notifier.Notify(ctx, e)
		})
		if nerr != nil {
			// 節目已入庫不會遺失,漏一次通知只記 log。
			slog.Error("notify failed", "title", e.Title, "err", nerr)
		}
	}
	return nil
}
```

- [ ] **Step 4: 跑測試確認通過**

Run: `go test ./internal/pipeline/ -v`
Expected: 8 個測試 PASS

- [ ] **Step 5: Commit**

```bash
git add internal/pipeline/
git commit -m "feat: pipeline orchestration with dual dedup and graceful degradation"
```

---

### Task 12: API(Gin handlers)

**Files:**
- Create: `internal/api/handlers.go`
- Create: `internal/api/router.go`
- Test: `internal/api/handlers_test.go`

- [ ] **Step 1: 寫失敗測試**

`internal/api/handlers_test.go`:

```go
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"gocrawler/internal/model"
	"gocrawler/internal/storage"
)

type fakeStore struct {
	events []model.Event
}

func (f *fakeStore) ListEvents(ctx context.Context, q, source string, limit, offset int) ([]model.Event, error) {
	return f.events, nil
}

func (f *fakeStore) GetEvent(ctx context.Context, id int64) (*model.Event, error) {
	for _, e := range f.events {
		if e.ID == id {
			return &e, nil
		}
	}
	return nil, storage.ErrNotFound
}

func TestListEvents(t *testing.T) {
	store := &fakeStore{events: []model.Event{{ID: 1, Title: "交響音樂會"}}}
	r := NewRouter(store)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/events?q=交響", nil))

	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Events []model.Event `json:"events"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Events, 1)
}

func TestGetEvent_Found(t *testing.T) {
	store := &fakeStore{events: []model.Event{{ID: 42, Title: "找得到"}}}
	r := NewRouter(store)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/events/42", nil))
	require.Equal(t, http.StatusOK, w.Code)
}

func TestGetEvent_NotFound(t *testing.T) {
	store := &fakeStore{}
	r := NewRouter(store)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/events/999", nil))
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetEvent_BadID(t *testing.T) {
	store := &fakeStore{}
	r := NewRouter(store)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/events/abc", nil))
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHealthz(t *testing.T) {
	r := NewRouter(&fakeStore{})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	require.Equal(t, http.StatusOK, w.Code)
}
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `go test ./internal/api/ -v`
Expected: FAIL,`NewRouter` 未定義

- [ ] **Step 3: 實作**

`internal/api/handlers.go`:

```go
package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"gocrawler/internal/model"
	"gocrawler/internal/storage"
)

type EventStore interface {
	ListEvents(ctx context.Context, q, source string, limit, offset int) ([]model.Event, error)
	GetEvent(ctx context.Context, id int64) (*model.Event, error)
}

type handlers struct {
	store EventStore
}

const (
	defaultLimit = 20
	maxLimit     = 100
)

func (h *handlers) listEvents(c *gin.Context) {
	limit, err := strconv.Atoi(c.DefaultQuery("limit", strconv.Itoa(defaultLimit)))
	if err != nil || limit < 1 || limit > maxLimit {
		limit = defaultLimit
	}
	offset, err := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if err != nil || offset < 0 {
		offset = 0
	}
	events, err := h.store.ListEvents(c.Request.Context(),
		c.Query("q"), c.Query("source"), limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	if events == nil {
		events = []model.Event{}
	}
	c.JSON(http.StatusOK, gin.H{"events": events})
}

func (h *handlers) getEvent(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	e, err := h.store.GetEvent(c.Request.Context(), id)
	if errors.Is(err, storage.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "event not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, e)
}

func (h *handlers) healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
```

`internal/api/router.go`:

```go
package api

import "github.com/gin-gonic/gin"

func NewRouter(store EventStore) *gin.Engine {
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())

	h := &handlers{store: store}
	r.GET("/events", h.listEvents)
	r.GET("/events/:id", h.getEvent)
	r.GET("/healthz", h.healthz)
	return r
}
```

- [ ] **Step 4: 跑測試確認通過**

Run: `go get github.com/gin-gonic/gin && go test ./internal/api/ -v`
Expected: 5 個測試 PASS

- [ ] **Step 5: Commit**

```bash
git add internal/api/ go.mod go.sum
git commit -m "feat: gin API with events endpoints"
```

---

### Task 13: cmd/app main(serve / scrape 子指令)

**Files:**
- Create: `cmd/app/main.go`
- Create: `embed.go`(repo 根,持有 migrations 的 embed.FS)

- [ ] **Step 1: embed migrations(repo 根層級)**

`embed.go`:

```go
// Package gocrawler 在 repo 根持有 migrations 的 embed.FS,
// 因為 go:embed 只能引用同 package 目錄下的檔案。
package gocrawler

import "embed"

//go:embed migrations/*.sql
var MigrationsFS embed.FS
```

- [ ] **Step 2: main.go**

`cmd/app/main.go`:

```go
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/robfig/cron/v3"

	gocrawler "gocrawler"
	"gocrawler/internal/api"
	"gocrawler/internal/config"
	"gocrawler/internal/matcher"
	"gocrawler/internal/notifier/discord"
	"gocrawler/internal/pipeline"
	"gocrawler/internal/scraper/opentix"
	"gocrawler/internal/storage"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 {
		return fmt.Errorf("usage: app <serve|scrape> [flags]")
	}
	cmd := os.Args[1]

	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	configPath := fs.String("config", "config.yaml", "設定檔路徑")
	noNotify := fs.Bool("no-notify", false, "只入庫不推播(初次 seed 用)")
	addr := fs.String("addr", ":8080", "API 監聽位址(serve 用)")
	if err := fs.Parse(os.Args[2:]); err != nil {
		return err
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}

	if err := storage.Migrate(gocrawler.MigrationsFS, cfg.DatabaseURL); err != nil {
		return err
	}
	store, err := storage.NewPostgresStore(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer store.Close()

	dedup, err := storage.NewDeduper(cfg.RedisAddr)
	if err != nil {
		return err
	}
	defer dedup.Close()

	p := &pipeline.Pipeline{
		Sources:  []pipeline.Source{opentix.New(cfg.OpentixURL)},
		Store:    store,
		Deduper:  dedup,
		Matcher:  matcher.New(cfg.Keywords),
		Notifier: discord.New(cfg.DiscordWebhookURL),
		Notify:   !*noNotify,
	}

	switch cmd {
	case "scrape":
		return p.Run(context.Background())
	case "serve":
		return serve(cfg, store, p, *addr)
	default:
		return fmt.Errorf("unknown command %q (want serve|scrape)", cmd)
	}
}

func serve(cfg *config.Config, store *storage.PostgresStore, p *pipeline.Pipeline, addr string) error {
	c := cron.New()
	if _, err := c.AddFunc(cfg.Cron, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		if err := p.Run(ctx); err != nil {
			slog.Error("scheduled run failed", "err", err)
		}
	}); err != nil {
		return fmt.Errorf("invalid cron expression %q: %w", cfg.Cron, err)
	}
	c.Start()
	defer c.Stop()

	srv := &http.Server{Addr: addr, Handler: api.NewRouter(store)}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	slog.Info("serving", "addr", addr, "cron", cfg.Cron)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-errCh:
		return err
	case <-quit:
		slog.Info("shutting down")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(ctx)
	}
}
```

- [ ] **Step 3: 安裝依賴並確認編譯與全部測試**

Run: `go get github.com/robfig/cron/v3 && go build ./... && go test ./...`
Expected: 編譯通過、全部測試 PASS(整合測試在無 env 時 skip)

- [ ] **Step 4: 端到端手動驗證**

```powershell
docker compose up -d
Copy-Item config.example.yaml config.yaml
# 編輯 config.yaml:opentix_url 填 Task 7 驗證的實際 endpoint、keywords 填自己的
$env:DISCORD_WEBHOOK_URL = "<你的 webhook url>"

go run ./cmd/app scrape -no-notify    # 初次 seed:只入庫
go run ./cmd/app scrape               # 第二次:應無新節目、無推播
go run ./cmd/app serve                # 起 API + 排程
```

另開視窗驗證:
```powershell
curl http://localhost:8080/healthz          # {"status":"ok"}
curl "http://localhost:8080/events?limit=5" # 有 seed 進去的節目
```

推播驗證:對 Postgres 刪掉一筆節目 + 刪掉對應 Redis key 後再 `scrape`,Discord 頻道應收到該節目通知(若標題命中關鍵字)。

- [ ] **Step 5: Commit**

```bash
git add cmd/ embed.go
git commit -m "feat: app entrypoint with serve/scrape subcommands"
```

---

### Task 14: Dockerfile 與收尾

**Files:**
- Create: `Dockerfile`
- Modify: `docker-compose.yml`(加入 app 服務)
- Create: `README.md`

- [ ] **Step 1: Dockerfile(multi-stage)**

`Dockerfile`:

```dockerfile
FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /app ./cmd/app

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /app /app
ENTRYPOINT ["/app"]
CMD ["serve"]
```

- [ ] **Step 2: docker-compose 加 app 服務**

`docker-compose.yml` 的 `services:` 下追加:

```yaml
  app:
    build: .
    depends_on:
      - postgres
      - redis
    environment:
      DATABASE_URL: "postgres://gocrawler:gocrawler@postgres:5432/gocrawler?sslmode=disable"
      REDIS_ADDR: "redis:6379"
      DISCORD_WEBHOOK_URL: "${DISCORD_WEBHOOK_URL}"
    volumes:
      - ./config.yaml:/config.yaml:ro
    command: ["serve", "-config", "/config.yaml"]
    ports:
      - "8080:8080"
```

- [ ] **Step 3: 驗證容器整組起得來**

Run: `docker compose up --build -d`,然後 `curl http://localhost:8080/healthz`
Expected: `{"status":"ok"}`

- [ ] **Step 4: README**

`README.md`(架構圖沿用 spec 第 3 節的資料流圖):

```markdown
# GoCrawler — OPENTIX 新節目上架通知

[![CI](https://github.com/<你的帳號>/<repo>/actions/workflows/ci.yml/badge.svg)](https://github.com/<你的帳號>/<repo>/actions)

定時抓取 OPENTIX 節目列表,新節目去重入庫,符合關鍵字即推播 Discord。

## 架構

(貼上 spec 第 3 節的資料流圖)

- 去重雙保險:Redis SETNX 快篩 + Postgres UNIQUE 最終防線
- 單一 binary:`serve`(API + cron)/ `scrape`(手動單次)

## 本地啟動

    docker compose up -d postgres redis
    cp config.example.yaml config.yaml   # 填入 keywords 與 opentix_url
    export DISCORD_WEBHOOK_URL=...
    go run ./cmd/app scrape -no-notify   # 初次 seed
    go run ./cmd/app serve

## API

| Method | Path | 說明 |
|--------|------|------|
| GET | /events?q=&source=&limit=&offset= | 節目列表(分頁、關鍵字過濾)|
| GET | /events/:id | 單一節目 |
| GET | /healthz | 健康檢查 |

## 測試

    go test ./...                        # 單元測試
    docker compose up -d postgres redis  # 整合測試需要真實 DB
    TEST_DATABASE_URL=... TEST_REDIS_ADDR=localhost:6379 go test ./...
```

README 中 `<你的帳號>/<repo>` 換成實際 GitHub 路徑。

- [ ] **Step 5: 最終驗證與 Commit**

Run: `go test ./...` + `go vet ./...`
Expected: 全部通過

```bash
git add Dockerfile docker-compose.yml README.md
git commit -m "feat: dockerfile, compose app service, README"
git push
```

---

## 自我檢查紀錄(Self-Review)

- **Spec 覆蓋**:spec §2 程式結構 → Task 2/7/10/12/13;§3 資料流與去重 → Task 9/11;§4 資料模型與設定 → Task 3/6/8;§5 錯誤處理 → Task 5/11(重試、降級、不中斷);§6 API 與測試 → Task 12 + 各 task 的 TDD 步驟與 Task 1 的 CI。無遺漏。
- **Placeholder**:唯一的不確定點(OPENTIX 實際 API 結構)已收斂為 Task 7 Step 1 的明確人工驗證程序與修正規則,非開放式 TBD。
- **型別一致性**:`InsertEvent(ctx, Event) (bool, error)`、`IsNew(ctx, source, id) (bool, error)`、`ListEvents(ctx, q, source, limit, offset)`、`GetEvent(ctx, id)` 在 storage 實作、pipeline/api 的消費端 interface、與各測試 fake 之間簽名一致;`storage.ErrNotFound` 由 api 與測試共用。
```
