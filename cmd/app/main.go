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
	noNotify := fs.Bool("no-notify", false, "不推播(初次 seed 或無 Discord 的 API-only 模式)")
	addr := fs.String("addr", ":8080", "API 監聽位址(serve 用)")
	if err := fs.Parse(os.Args[2:]); err != nil {
		return err
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}

	// 通知模式缺 webhook 是設定錯誤:與其之後每筆推播都失敗,不如啟動即失敗。
	if !*noNotify && cfg.DiscordWebhookURL == "" {
		return fmt.Errorf("discord_webhook_url is empty; set DISCORD_WEBHOOK_URL or pass -no-notify")
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
		// Ctrl+C 走測過的 ctx 取消路徑(含 detached Forget),而非硬死。
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		return p.Run(ctx)
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

	// job ctx derive 自 serve 生命週期:shutdown 時 stopRuns() 取消 in-flight run,
	// 讓它走已測過的取消 + detached Forget 路徑,而不是被遺棄。
	runCtx, stopRuns := context.WithCancel(context.Background())
	defer stopRuns()

	// v3.0.1 的 New() 預設不 recover,p.Run panic 會帶崩整個 serve。
	c := cron.New(cron.WithChain(cron.Recover(cron.DefaultLogger)))
	if _, err := c.AddFunc(cfg.Cron, func() {
		ctx, cancel := context.WithTimeout(runCtx, 10*time.Minute)
		defer cancel()
		if err := p.Run(ctx); err != nil {
			slog.Error("scheduled run failed", "err", err)
		}
	}); err != nil {
		return fmt.Errorf("invalid cron expression %q: %w", cfg.Cron, err)
	}
	c.Start()

	srv := &http.Server{Addr: addr, Handler: api.NewRouter(store)}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	slog.Info("serving", "addr", addr, "cron", cfg.Cron, "notify", p.Notify)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-errCh:
		return err
	case <-quit:
		slog.Info("shutting down")
		stopRuns()
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err := srv.Shutdown(shutCtx)
		// c.Stop() 回傳的 ctx 在所有執行中 job 結束時 Done:等 in-flight run 排空。
		select {
		case <-c.Stop().Done():
		case <-shutCtx.Done():
			slog.Warn("cron run still in flight at shutdown deadline")
		}
		return err
	}
}
