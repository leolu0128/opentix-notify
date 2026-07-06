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
	OpentixCategories []string `yaml:"opentix_categories"`
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
