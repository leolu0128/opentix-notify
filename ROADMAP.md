# GitHub 履歷項目計畫:Go 爬蟲資料聚合平台

> 目標:打造一個可持續維護、能作為求職履歷亮點的後端項目
> 定位:學生 / 找第一份後端(全端)工作
> 技術主軸:Go

---

## 一、為什麼選這個項目

選擇「解決自己真實問題的工具」是可持續維護的關鍵——只有你真的在用,才會持續 commit,面試官也能一眼看出這是認真項目而非教學跟做。

爬蟲 + 資料聚合平台(例如租屋資訊聚合、二手商品比價、演唱會票務通知)的優勢:

- **天然可持續**:資料源會改版,你就有持續維護的理由
- **技術展示面廣**:排程任務、併發抓取、資料清洗、快取、通知系統
- **好講故事**:題材貼近生活,面試時容易展開
- **Go 的語言優勢**:goroutine 併發抓取、channel、rate limiting 都是後端面試高頻考點

## 二、技術棧

| 模組 | 選型 | 說明 |
|------|------|------|
| 爬蟲 | net/http 打 JSON API(OPENTIX);Colly 用於 HTML 解析(tixCraft) | chromedp 保留給未來 JavaScript 渲染的網站 |
| 後端框架 | Gin(或 Echo) | REST API |
| 資料庫 | PostgreSQL | 永久儲存與查詢 |
| 快取 / 去重 | Redis | 判斷是否為新資料 |
| 排程 | robfig/cron + goroutine | 定時觸發、併發抓取 |
| 通知 | Discord Webhook(二階段升級 Discord Bot) | 新資料即時推播;Line Notify 已於 2025-04 停止服務 |
| Migration | golang-migrate | 資料庫版本管理 |
| CI/CD | GitHub Actions | 自動跑 `go test` + `golangci-lint` |
| 部署 | Docker + docker-compose,上線用 Fly.io 或 VPS | 一鍵啟動 app + Postgres + Redis |
| 前端(選配) | Vue 或 React | 簡單的查詢頁面 |

## 三、系統架構(第一版:單體)

```
資料來源(租屋、票務等目標網站)
        │
        ▼
爬蟲排程與抓取(cron 觸發、goroutine 併發)
        │
        ▼
清洗與去重(Redis 判斷是否為新資料)
        │
        ▼
   PostgreSQL(永久儲存與查詢)
        │
   ┌────┴────┐
   ▼         ▼
API 服務    通知服務
(Gin)   (Telegram Bot)
```

資料流:排程器定時喚醒 goroutine 併發抓取目標網站 → 抓回的資料先過 Redis 去重 → 新資料寫入 PostgreSQL,同時觸發 Telegram 推播 → Gin 提供 REST API 供查詢歷史資料。

## 四、Repo 結構

```
project/
├── cmd/app/main.go        # 單一入口:serve(API+排程)| scrape(手動單次抓取)
├── internal/
│   ├── scraper/           # Source interface + 各平台實作(OPENTIX → tixCraft)
│   ├── storage/           # Postgres/Redis 存取層
│   ├── notifier/          # Notifier interface + Discord Webhook 實作
│   ├── matcher/           # 關鍵字比對
│   └── api/               # Gin handlers
├── migrations/
├── docker-compose.yml
└── .github/workflows/ci.yml
```

單一 binary 讓部署只需一台機器(PaaS 拆兩服務費用翻倍);未來要拆分時,`internal/` 的分層讓拆分成本很低。

`cmd` + `internal` 是 Go 社群的標準佈局,能展示你熟悉語言慣例。

## 五、開發路線(約一個月)

### 第一週:骨架與基礎建設

- 初始化 Go module
- 設計 PostgreSQL schema,用 golang-migrate 管理 migration
- 用 Gin 寫出 2~3 支基本 CRUD API
- 從第一天就寫測試
- 設定 GitHub Actions:自動跑 `go test` 與 `golangci-lint`,README 掛上 CI 徽章

### 第二週:爬蟲核心

- 針對第一個資料源 OPENTIX 實作抓取邏輯(JSON API,不需 Colly;第二資料源 tixCraft 才需要 HTML 解析)
- Worker pool:用 channel 控制併發數
- Rate limiting:對目標網站保持禮貌(也是面試好話題)
- 失敗重試機制
- 手動觸發跑通「抓取 → 清洗 → 入庫」完整流程

### 第三週:去重、排程與通知

- 接入 Redis 做資料去重
- 用 robfig/cron 實作定時排程
- 接 Discord Webhook:有新資料且符合關鍵字即推播
- 里程碑:產品「活」起來,自己每天收到通知 → 持續維護的動力來源

### 第四週:部署上線與文件

- 撰寫 Dockerfile 與 docker-compose(app + Postgres + Redis 一鍵啟動)
- 部署到 Fly.io 或便宜 VPS
- 補齊 README:架構圖、API 文件、本地啟動教學

## 六、讓項目「像履歷」的關鍵做法

1. **README 要專業**:架構圖、API 文件、如何本地跑起來——面試官通常只看 README 就決定要不要點進 code
2. **有 CI/CD**:GitHub Actions 跑測試 + 自動部署,一天就能設好,但很多學生沒做,是很好的差異化
3. **Commit 紀錄乾淨**:規律的小 commit、有意義的訊息,勝過一次性倒入的大 commit
4. **實際部署上線**:「這裡可以試用」比「理論上能跑」有力得多

## 七、後續演進方向

- **新增第二個資料源**:會逼你把 scraper 抽象成 interface,是自然的重構練習,也是 commit 持續增長的來源
- **加上前端查詢頁面**(Vue / React):往全端方向補強
- **單體 → 微服務**:等單體穩定運行幾個月後,可拆分為 Python 爬蟲 + Go API,中間用訊息佇列(RabbitMQ / Kafka)串接——「從單體演進到微服務」本身就是很好的面試故事(注意:一開始不要過度設計)

## 八、面試可展開的技術話題

- goroutine / channel 併發模型與 worker pool 設計
- Rate limiting 的實作方式
- Redis 快取與去重策略
- RESTful API 設計與分層架構
- 資料庫 schema 設計與 migration 管理
- CI/CD pipeline 與容器化部署
