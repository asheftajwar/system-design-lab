package cache

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestRedisCacheGetSetDelete(t *testing.T) {
	ctx := context.Background()

	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
	defer client.Close()

	cache := &RedisCache{
		client: client,
		ttl:    time.Minute,
	}

	key := "test:url:cache"
	value := "https://example.com"

	t.Cleanup(func() {
		_ = cache.Delete(ctx, key)
	})

	if err := cache.Set(ctx, key, value); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	got, err := cache.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if got != value {
		t.Fatalf("Get() = %q, want %q", got, value)
	}

	if err := cache.Delete(ctx, key); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	_, err = cache.Get(ctx, key)
	if err != ErrNotFound {
		t.Fatalf("Get() after Delete() error = %v, want %v", err, ErrNotFound)
	}
}

func TestRedisCacheTTL(t *testing.T) {
	ctx := context.Background()

	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
	defer client.Close()

	cache := &RedisCache{
		client: client,
		ttl:    1 * time.Second,
	}

	key := "test:url:ttl"
	value := "https://example.com/ttl"

	t.Cleanup(func() {
		_ = cache.Delete(ctx, key)
	})

	if err := cache.Set(ctx, key, value); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	if _, err := cache.Get(ctx, key); err != nil {
		t.Fatalf("Get() immediately after Set() error = %v", err)
	}

	time.Sleep(1100 * time.Millisecond)

	_, err := cache.Get(ctx, key)
	if err != ErrNotFound {
		t.Fatalf("Get() after TTL expiration error = %v, want %v", err, ErrNotFound)
	}
}
