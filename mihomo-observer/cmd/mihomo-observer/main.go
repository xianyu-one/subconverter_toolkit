package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
	_ "time/tzdata"

	"mihomo-observer/internal/api"
	"mihomo-observer/internal/collector"
	"mihomo-observer/internal/config"
	"mihomo-observer/internal/storage/sqlite"
)

func main() {
	path := flag.String("config", "config.yaml", "configuration file")
	flag.Parse()
	cfg, err := config.Load(*path)
	if err != nil {
		slog.Error("configuration failed", "error", err)
		os.Exit(1)
	}
	store, err := sqlite.Open(cfg.Database.Path, cfg.ReportingTimezone)
	if err != nil {
		slog.Error("database failed", "error", err)
		os.Exit(1)
	}
	defer store.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var workers sync.WaitGroup
	c := collector.New(cfg.Mihomo.API, cfg.Mihomo.Secret, time.Duration(cfg.Collector.IntervalMS)*time.Millisecond, store)
	if cfg.CollectEnabled() {
		workers.Add(1)
		go func() { defer workers.Done(); c.Run(ctx) }()
	}
	workers.Add(1)
	go func() {
		defer workers.Done()
		projectionTicker := time.NewTicker(30 * time.Second)
		analysisTicker := time.NewTicker(5 * time.Minute)
		defer projectionTicker.Stop()
		defer analysisTicker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-projectionTicker.C:
				if err := store.Project(ctx, time.Now().UnixMilli()); err != nil {
					slog.Error("projection failed", "error", err)
				}
			case <-analysisTicker.C:
				if !cfg.AnalyzeEnabled() {
					continue
				}
				if err := store.Project(ctx, time.Now().UnixMilli()); err != nil {
					slog.Error("projection failed", "error", err)
					continue
				}
				analysisCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
				if err := store.Analyze(analysisCtx, time.Now().UnixMilli()); err != nil {
					slog.Error("analysis failed", "error", err)
				}
				cancel()
			}
		}
	}()
	workers.Add(1)
	go func() {
		defer workers.Done()
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := store.Cleanup(ctx, time.Now().UnixMilli(), cfg.Retention.RawDays, cfg.Retention.HourlyDays, cfg.Retention.DailyDays); err != nil {
					slog.Error("cleanup failed", "error", err)
				}
			}
		}
	}()
	slog.Info("observer API listening", "address", cfg.Web.Listen)
	err = api.Serve(ctx, cfg.Web.Listen, api.New(store, cfg.Web.Password, c.Status).Handler())
	stop()
	workers.Wait()
	if err != nil {
		slog.Error("server failed", "error", err)
		os.Exit(1)
	}
}
