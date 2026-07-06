# Task 7 修訂:OPENTIX Source 改用 search API

> 原計畫(2026-07-03-opentix-notify.md Task 7)假設用節目列表 GET API。
> 2026-07-06 以 DevTools 實測後,改採 search API——多了場館名稱與開賣時間
> (直接對應 schema 的 on_sale_time,供第二階段開賣提醒使用)。本文件取代原 Task 7。

## 實測結論(2026-07-06)

- **Endpoint**:`POST https://search.opentix.life/search`,匿名可用(不需登入 token)
- **Request body**(Content-Type: application/json):

```json
{
  "language": "zh-CHT",
  "categoryFilter": ["音樂-管絃樂團", "..."],
  "sortBy": "ABOUT_TO_BEGIN",
  "offset": 0
}
```

- `categoryFilter` **必須非空**,空陣列或缺欄位回 400
- 分頁:`offset` 進、`result.nextOffset` / `result.hitsCount` 出,一頁 15 筆
- 時間戳為**毫秒**(`/programs` API 是秒,勿混淆)
- 回應結構:`result.found[].source` 內含 `id`(字串)、`title`、`displayCategory`、
  `startDateTime`、`onlineStartDateTime`(開賣)、`eventVenues[]`(`city`+`name`+`times[]`)、`places[]`
- 節目頁 URL:`https://www.opentix.life/event/{id}`

## 欄位對映

| model.Event | 來源 |
|---|---|
| Source | `"opentix"` |
| SourceEventID | `source.id` |
| Title | `source.title` |
| URL | `"https://www.opentix.life/event/" + id` |
| Venue | 單一場館:`"{city} {name}"`;多場館:`strings.Join(places, "、")` |
| StartTime | `time.UnixMilli(source.startDateTime)`,缺(0)則 nil |
| OnSaleTime | `time.UnixMilli(source.onlineStartDateTime)`,缺(0)則 nil |
| Raw | `found[].source` 的原始 JSON |

## 抓取策略

- `sortBy: "ABOUT_TO_BEGIN"` + 全量分頁掃描(順序不影響正確性,去重靠 Redis/Postgres)
- 停止條件:`found` 為空、或 `nextOffset <= offset`(防迴圈)、或 `offset >= hitsCount`、或達 `maxPages`(40 頁 = 600 筆,現況 488 筆有餘裕)
- 禮貌:頁間停頓 500ms(respect ctx)、30s timeout、自報 User-Agent;每小時一輪約 33 個請求
- `categoryFilter` 來自設定檔新欄位 `opentix_categories`(非空驗證在建構子)

## Config 變更

- `Config` struct 新增 `OpentixCategories []string \`yaml:"opentix_categories"\``
- `config.example.yaml`:`opentix_url` 改為 `https://search.opentix.life/search`,新增 `opentix_categories`(預設列音樂類 17 分類)

## 檔案

- Create: `internal/scraper/source.go`(Source interface,同原計畫)
- Create: `internal/scraper/opentix/opentix.go`
- Create: `internal/scraper/opentix/testdata/search_page1.json`、`search_page2.json`(自真實回應裁剪,兩頁構成完整分頁情境)
- Modify: `internal/config/config.go`、`internal/config/config_test.go`、`config.example.yaml`

## 測試(TDD)

1. fixture 兩頁分頁走完:3 筆 Event,欄位對映逐一斷言(含毫秒轉換、開賣時間)
2. 多場館 → Venue 用 places 連接(`高雄、臺中`)
3. 缺 `startDateTime`/`onlineStartDateTime` → 對應欄位 nil
4. 請求體斷言:categoryFilter/language/sortBy/offset 有送出
5. HTTP 500 → error;非 JSON → error
6. `nextOffset` 不前進 → 停止(防無窮迴圈)
7. maxPages 上限生效(測試中把頁間延遲設 0)
8. 空 categories → `New` 回傳 error
