package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/asheftajwar/system-design-lab/01-url-shortener/internal/analytics"
	"github.com/asheftajwar/system-design-lab/01-url-shortener/internal/cache"
	"github.com/asheftajwar/system-design-lab/01-url-shortener/internal/config"
	"github.com/asheftajwar/system-design-lab/01-url-shortener/internal/handler"
	"github.com/asheftajwar/system-design-lab/01-url-shortener/internal/metrics"
	"github.com/asheftajwar/system-design-lab/01-url-shortener/internal/repository"
	"github.com/asheftajwar/system-design-lab/01-url-shortener/internal/service"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	cfg := config.Load()

	ctx := context.Background()

	poolConfig, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("failed to parse database config: %v", err)
	}

	poolConfig.MaxConns = 10
	poolConfig.MinConns = 2
	poolConfig.MaxConnLifetime = time.Hour

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		log.Fatalf("failed to create database pool: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("failed to ping database: %v", err)
	}

	log.Println("connected to PostgreSQL")

	urlRepository := repository.NewPostgresURLRepository(pool)
	analyticsRepository := repository.NewPostgresAnalyticsRepository(pool)

	urlCache, err := cache.NewRedisCache(cfg.RedisURL, 24*time.Hour)
	if err != nil {
		log.Fatalf("failed to create Redis cache: %v", err)
	}
	defer urlCache.Close()

	if err := urlCache.Ping(ctx); err != nil {
		log.Fatalf("failed to ping Redis: %v", err)
	}

	log.Println("connected to Redis")

	appMetrics := metrics.New()
	serviceMetrics := metrics.NewServiceMetrics(appMetrics)

	registry := prometheus.NewRegistry()

	if err := appMetrics.Register(registry); err != nil {
		log.Fatalf("failed to register metrics: %v", err)
	}

	analyticsWorker := analytics.NewWorker(
		analyticsRepository,
		analytics.WorkerConfig{
			BufferSize:  1000,
			FlushSize:   100,
			FlushPeriod: time.Second,
		},
	)

	workerCtx, workerCancel := context.WithCancel(context.Background())
	workerDone := make(chan error, 1)

	go func() {
		workerDone <- analyticsWorker.Run(workerCtx)
	}()

	log.Println("analytics worker started")

	urlService := service.NewURLService(
		urlRepository,
		cfg.BaseURL,
		urlCache,
	).
		WithMetrics(serviceMetrics).
		WithAnalytics(analyticsWorker).
		WithAnalyticsRepository(analyticsRepository)

	urlHandler := handler.NewURLHandler(urlService)

	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.Handle("GET /metrics", promhttp.HandlerFor(
		registry,
		promhttp.HandlerOpts{},
	))

	mux.HandleFunc("POST /v1/urls", urlHandler.CreateURL)
	mux.HandleFunc("GET /{code}", urlHandler.Redirect)
	mux.HandleFunc("GET /v1/urls/{code}", urlHandler.GetMetadata)

	server := &http.Server{
		Addr:    ":8080",
		Handler: metrics.Middleware(appMetrics)(mux),
	}

	log.Printf("server listening on %s", server.Addr)

	// Listen for operating-system shutdown signals.
	signalCtx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	serverErr := make(chan error, 1)

	go func() {
		serverErr <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErr:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server failed: %v", err)
		}

	case <-signalCtx.Done():
		log.Println("shutdown signal received")
	}

	// Stop accepting new HTTP requests and wait for active requests
	// to finish.
	shutdownCtx, shutdownCancel := context.WithTimeout(
		context.Background(),
		10*time.Second,
	)

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("HTTP server shutdown failed: %v", err)
	}

	shutdownCancel()

	// Stop analytics ingestion and flush accepted events.
	workerCancel()

	select {
	case err := <-workerDone:
		if err != nil {
			log.Printf("analytics worker shutdown failed: %v", err)
		} else {
			log.Println("analytics worker stopped")
		}

	case <-time.After(6 * time.Second):
		log.Println("analytics worker shutdown timed out")
	}

	log.Println("server stopped")
}