package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/asheftajwar/system-design-lab/01-url-shortener/internal/cache"
	"github.com/asheftajwar/system-design-lab/01-url-shortener/internal/config"
	"github.com/asheftajwar/system-design-lab/01-url-shortener/internal/handler"
	"github.com/asheftajwar/system-design-lab/01-url-shortener/internal/repository"
	"github.com/asheftajwar/system-design-lab/01-url-shortener/internal/service"
	"github.com/jackc/pgx/v5/pgxpool"
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
	urlCache, err := cache.NewRedisCache(cfg.RedisURL, 24*time.Hour)
	if err != nil {
		log.Fatalf("failed to create Redis cache: %v", err)
	}
	defer urlCache.Close()

	if err := urlCache.Ping(ctx); err != nil {
		log.Fatalf("failed to ping Redis: %v", err)
	}

	log.Println("connected to Redis")
	urlService := service.NewURLService(urlRepository, cfg.BaseURL, urlCache)
	urlHandler := handler.NewURLHandler(urlService)

	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("POST /v1/urls", urlHandler.CreateURL)
	mux.HandleFunc("GET /{code}", urlHandler.Redirect)

	server := &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	log.Printf("server listening on %s", server.Addr)

	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
