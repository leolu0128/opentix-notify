# opentix-notify

[![CI](https://github.com/leolu0128/opentix-notify/actions/workflows/ci.yml/badge.svg)](https://github.com/leolu0128/opentix-notify/actions/workflows/ci.yml)

OPENTIX(兩廳院售票)新節目上架通知。每小時抓取節目列表,新節目去重入庫,標題命中關鍵字即推播 Discord;另提供查詢 API。Go 實作,單一 binary。

## 架構

```
cron(每小時)/ scrape 子指令
        │
        ▼
OPENTIX search API(offset 分頁全量掃描,頁間 500ms)
        │
        ▼
正規化 Event(含場館、演出時間、開賣時間)
        │
        ▼
Redis SET NX 快篩 ──(Redis 故障時降級)──┐
        │(新的才往下)                    │
        ▼                                  ▼
Postgres INSERT .. ON CONFLICT DO NOTHING(去重最終防線)
        │(insert 成功才通知)
        ▼
關鍵字比對 → Discord Webhook(重試 3 次,失敗不中斷)
```

- **去重雙保險**:Redis `SET NX`(90 天 TTL)快篩 + Postgres `UNIQUE(source, source_event_id)` 最終判準;insert 失敗會撤銷 Redis 標記,不會漏報
- **單一 binary 兩種模式**:`serve`(API + cron 排程)/`scrape`(手動單次)
- **韌性**:抓取與推播各 3 次指數退避重試、單筆壞資料跳過不全黑、graceful shutdown 會等進行中的抓取收尾

## 本地啟動

需求:Go 1.23+、Docker

```sh
docker compose up -d postgres redis
cp config.example.yaml config.yaml        # 編輯 keywords(關鍵字)等設定
go run ./cmd/app scrape -no-notify        # 初次 seed:只入庫不推播,避免洗頻
export DISCORD_WEBHOOK_URL="https://discord.com/api/webhooks/..."
go run ./cmd/app serve                    # API + 每小時排程
```

> Windows(PowerShell)以 `$env:DISCORD_WEBHOOK_URL = "..."` 設定環境變數。
> 本機 5432 若被其他 PostgreSQL 佔用:compose 已把容器映射到 **15432**,`config.example.yaml` 的連線字串已對應。

沒有 Discord webhook 也能跑:`go run ./cmd/app serve -no-notify`(API-only 模式)。

## Docker 一鍵啟動

```sh
cp config.example.yaml config.yaml
export DISCORD_WEBHOOK_URL="..."
docker compose up --build -d
curl http://localhost:8080/healthz
```

## API

| Method | Path | 說明 |
|--------|------|------|
| GET | `/events?q=&source=&limit=&offset=` | 節目列表(標題關鍵字過濾、分頁,limit 上限 100) |
| GET | `/events/:id` | 單一節目 |
| GET | `/healthz` | 健康檢查 |

## 設定

`config.yaml`(參考 `config.example.yaml`):

| 欄位 | 說明 |
|------|------|
| `keywords` | 推播關鍵字(標題不分大小寫包含即命中;空清單不推播) |
| `cron` | 抓取排程(預設每小時整點) |
| `opentix_url` / `opentix_categories` | search API 位址與分類過濾(不可為空) |
| `database_url` / `redis_addr` | 可用環境變數 `DATABASE_URL` / `REDIS_ADDR` 覆蓋 |
| `discord_webhook_url` | 建議一律用環境變數 `DISCORD_WEBHOOK_URL` 提供 |

## 測試

```sh
go test ./...                                  # 單元測試(不需外部服務)
docker compose up -d postgres redis            # 整合測試需要真實 DB
TEST_DATABASE_URL="postgres://gocrawler:gocrawler@localhost:15432/gocrawler?sslmode=disable" \
TEST_REDIS_ADDR="localhost:6379" go test ./...
```

## Roadmap

- [x] OPENTIX 新節目上架通知(v1)
- [ ] 開賣時間提醒(資料已入庫,`on_sale_time` 欄位)
- [ ] 第二資料源:拓元 tixCraft(`Source` interface 已預留)
- [ ] Discord Bot 訂閱指令(`/subscribe 關鍵字`)
