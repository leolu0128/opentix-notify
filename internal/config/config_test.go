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
opentix_categories: [音樂-管絃樂團, 音樂-獨奏]
database_url: "postgres://u:p@localhost:5432/db"
redis_addr: "localhost:6379"
discord_webhook_url: "https://discord.com/api/webhooks/x"
`)
	cfg, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, []string{"交響", "鋼琴"}, cfg.Keywords)
	require.Equal(t, "0 * * * *", cfg.Cron)
	require.Equal(t, "https://example.com/api", cfg.OpentixURL)
	require.Equal(t, []string{"音樂-管絃樂團", "音樂-獨奏"}, cfg.OpentixCategories)
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
