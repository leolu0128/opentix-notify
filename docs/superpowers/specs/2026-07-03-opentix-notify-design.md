# 設計文件:OPENTIX 新節目上架通知(GoCrawler 第一版)

> 日期:2026-07-03
> 狀態:已確認
> 對應計畫:ROADMAP.md 的第一個資料源實作

## 1. 範圍與決策總覽

第一版(約四週)只做一件事:**OPENTIX 兩廳院售票的新節目上架通知**。
定時抓 OPENTIX 節目列表 → 去重 → 新節目入庫 → 符合關鍵字就推 Discord。

已確認的決策:

| 決策點 | 選擇 | 理由 |
|--------|------|------|
| 通知類型 | 新節目上架(A) | 列表輪詢一小時一次即可,對網站禮貌,一週可跑通 |
| 第一資料源 | OPENTIX | SPA 背後有結構化 JSON API,不需解析 HTML 或 chromedp |
| 過濾方式 | 關鍵字過濾(設定檔) | 全推會變垃圾訊息;訂閱系統留到二階段 |
| 通知管道 | Discord Webhook | 單向推播一支 HTTP POST 搞定,不需 Bot token(Line Notify 已於 2025-04 停止服務) |
| 執行檔結構 | 單一 main + 子指令 | 部署只需一台機器(PaaS 拆兩服務費用翻倍);`scrape` 子指令方便開發除錯 |

第二階段(本文件不涵蓋,僅列方向):

- 開賣時間提醒(開賣前 N 分鐘推播)
- 第二資料源:拓元 tixCraft(HTML 解析,驗證 `Source` interface 抽象)
- Discord Bot 訂閱指令(`/subscribe 關鍵字`,關鍵字入庫)

## 2. 程式結構

單一 binary、兩個子指令,用標準函式庫 `flag` + subcommand,不引入 cobra:

- `./app serve` — 啟動 Gin API + cron 排程(各自 goroutine)
- `./app scrape` — 手動觸發單次抓取,與排程共用同一條 pipeline

```
gocrawler/
├── cmd/app/main.go          # serve | scrape 兩個子指令
├── internal/
│   ├── scraper/             # Source interface + opentix 實作
│   ├── storage/             # Postgres(sqlx 或 pgx)+ Redis
│   ├── notifier/            # Notifier interface + Discord Webhook 實作
│   ├── matcher/             # 關鍵字比對
│   ├── api/                 # Gin handlers
│   └── config/              # 設定載入
├── migrations/
├── docker-compose.yml
└── .github/workflows/ci.yml
```

兩個核心 interface,是未來擴充的接縫:

```go
type Source interface {
    Name() string
    Fetch(ctx context.Context) ([]Event, error)
}

type Notifier interface {
    Notify(ctx context.Context, e Event) error
}
```

二階段加 tixCraft = 多一個 `Source` 實作;換 Discord Bot = 多一個 `Notifier` 實作,主流程不動。

## 3. 資料流與去重

```
cron(每小時)或 scrape 子指令
        │
        ▼
Fetch:打 OPENTIX 節目列表 JSON API
        │
        ▼
正規化為 Event struct
        │
        ▼
Redis SETNX dedup:opentix:<節目ID>   ← 第一線快篩
        │(新的才往下)
        ▼
INSERT INTO events(UNIQUE 衝突則跳過)← 最終防線
        │(insert 成功才往下)
        ▼
關鍵字比對(matcher)
        │(命中才往下)
        ▼
Discord Webhook 推播
```

去重雙保險:Redis 掉資料也不會重複推播,因為 Postgres `UNIQUE(source, source_event_id)` insert 衝突就不觸發通知。

OPENTIX 的實際 endpoint 與回應格式在實作第一步用瀏覽器 DevTools 確認,並把一份真實回應存成測試 fixture。

## 4. 資料模型與設定

`events` 表:

| 欄位 | 型別 | 說明 |
|------|------|------|
| id | bigserial PK | |
| source | text | 固定 `opentix`,二階段會有 `tixcraft` |
| source_event_id | text | 平台方的節目 ID |
| title | text | 節目名稱 |
| url | text | 節目頁連結 |
| venue | text | 場地(可空) |
| start_time | timestamptz | 演出時間(可空) |
| on_sale_time | timestamptz | 開賣時間(可空,二階段用) |
| raw | jsonb | 原始 JSON,之後想多解析欄位不用重抓 |
| created_at | timestamptz | |

約束:`UNIQUE(source, source_event_id)`。

設定:YAML 檔 + 環境變數覆蓋(secrets 如 webhook URL、DB 密碼一律走 env)。內容:關鍵字清單、cron 排程、Postgres/Redis DSN、Discord webhook URL。

## 5. 錯誤處理

- **抓取失敗**:重試 3 次(指數退避),仍失敗記 log 等下一輪;排程任務絕不讓整個程式掛掉
- **推播失敗**:重試 3 次後放棄並 log;節目已入庫不會遺失,只是漏一次通知
- **回應格式改版**:解析失敗視同抓取失敗,log 明確指出解析錯在哪──這是「持續維護的理由」的來源
- **禮貌爬蟲**:每小時一次、單一請求、正常 User-Agent,不做高頻輪詢

## 6. API 與測試

第一版 API 三支:

- `GET /events` — 分頁,支援 `?q=`(標題關鍵字)與 `?source=` 過濾
- `GET /events/:id`
- `GET /healthz`

測試策略:

| 對象 | 方式 |
|------|------|
| OPENTIX parser | 真實回應存 fixture,測正規化 |
| matcher | 純函式,直接單元測試 |
| pipeline | mock Source/Notifier,測「新資料才通知」邏輯 |
| storage | 整合測試,接 docker-compose 起的真 Postgres |
| CI | GitHub Actions:`go test` + `golangci-lint` |
