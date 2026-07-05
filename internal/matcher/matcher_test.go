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
