package matcher

import "strings"

// Matcher 以關鍵字清單對節目標題做不分大小寫的包含比對。
type Matcher struct {
	keywords []string // 已轉小寫
}

// New 建立關鍵字比對器。keywords 為空時 Match 一律回傳 false。
func New(keywords []string) *Matcher {
	lowered := make([]string, 0, len(keywords))
	for _, k := range keywords {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		lowered = append(lowered, strings.ToLower(k))
	}
	return &Matcher{keywords: lowered}
}

// Match 回報 title 是否(不分大小寫)包含任一關鍵字。
func (m *Matcher) Match(title string) bool {
	t := strings.ToLower(title)
	for _, k := range m.keywords {
		if strings.Contains(t, k) {
			return true
		}
	}
	return false
}
