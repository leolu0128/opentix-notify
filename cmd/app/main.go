// gocrawler 的入口:serve 啟動 API 與排程,scrape 執行單次抓取。
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

	"github.com/gin-gonic/gin"
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
	defer func() { _ = store.Close() }()

	dedup, err := storage.NewDeduper(cfg.RedisAddr)
	if err != nil {
		return err
	}
	defer func() { _ = dedup.Close() }()

	src, err := opentix.New(cfg.OpentixURL, cfg.OpentixCategories)
	if err != nil {
		return err
	}

	p := &pipeline.Pipeline{
		Sources:  []pipeline.Source{src},
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
	// library 層不碰 process-global 的 gin mode,由入口統一設定;
	// 外部有設 GIN_MODE 時尊重之。
	if os.Getenv("GIN_MODE") == "" {
		gin.SetMode(gin.ReleaseMode)
	}

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
