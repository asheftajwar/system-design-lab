package config

import "os"

type Config struct {
	DatabaseURL string
}

func Load() Config {
	return Config{
		DatabaseURL: getEnv(
			"DATABASE_URL",
			"postgres://shortener:shortener@localhost:5432/shortener",
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