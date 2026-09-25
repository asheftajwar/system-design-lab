package config

import "os"

type Config struct {
	DatabaseURL string
	RedisURL    string
	BaseURL     string
}

func Load() Config {
	return Config{
		DatabaseURL: getEnv(
			"DATABASE_URL",
			"postgres://shortener:shortener@localhost:5432/shortener",
		),
		RedisURL: getEnv(
			"REDIS_URL",
			"redis://localhost:6379",
		),
		BaseURL: getEnv(
			"BASE_URL",
			"http://localhost:8080",
		),
	}
}

func getEnv(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}