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
