package limiter

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func newRedisClusterIntegrationClient(t *testing.T) *redis.ClusterClient {
	t.Helper()

	rawAddrs := os.Getenv("REDIS_CLUSTER_ADDRS")
	if rawAddrs == "" {
		t.Skip("set REDIS_CLUSTER_ADDRS to run Redis Cluster integration tests")
	}

	addrs := strings.Split(rawAddrs, ",")
	for i := range addrs {
		addrs[i] = strings.TrimSpace(addrs[i])
	}

	client := redis.NewClusterClient(&redis.ClusterOptions{
		Addrs: addrs,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		t.Fatalf("Redis Cluster ping failed: %v", err)
	}

	t.Cleanup(func() {
		_ = client.Close()
	})

	return client
}

func TestRedisClusterTokenBucket(t *testing.T) {
	client := newRedisClusterIntegrationClient(t)
	ctx := context.Background()

	bucket, err := NewRedisTokenBucket(client)
	if err != nil {
		t.Fatalf("create token bucket: %v", err)
	}

	key := "cluster-token-bucket-" + time.Now().Format("150405.000000000")

	first, err := bucket.Allow(ctx, key, 3, 1, 2)
	if err != nil {
		t.Fatalf("first Allow: %v", err)
	}
	if !first.Allowed {
		t.Fatalf("first request should be allowed: %+v", first)
	}

	second, err := bucket.Allow(ctx, key, 3, 1, 2)
	if err != nil {
		t.Fatalf("second Allow: %v", err)
	}
	if second.Allowed {
		t.Fatalf("second request should be rejected: %+v", second)
	}
}

func TestRedisClusterFixedWindow(t *testing.T) {
	client := newRedisClusterIntegrationClient(t)
	ctx := context.Background()

	window := NewRedisFixedWindow(client)
	key := "cluster-fixed-window-" + time.Now().Format("150405.000000000")

	first, err := window.Allow(ctx, key, 2, 60, 2)
	if err != nil {
		t.Fatalf("first Allow: %v", err)
	}
	if !first.Allowed {
		t.Fatalf("first request should be allowed: %+v", first)
	}

	second, err := window.Allow(ctx, key, 2, 60, 1)
	if err != nil {
		t.Fatalf("second Allow: %v", err)
	}
	if second.Allowed {
		t.Fatalf("second request should be rejected: %+v", second)
	}
}

func TestRedisClusterSlidingWindow(t *testing.T) {
	client := newRedisClusterIntegrationClient(t)
	ctx := context.Background()

	window := NewRedisSlidingWindow(client)
	key := "cluster-sliding-window-" + time.Now().Format("150405.000000000")

	first, err := window.Allow(ctx, key, 2, 60, 2)
	if err != nil {
		t.Fatalf("first Allow: %v", err)
	}
	if !first.Allowed {
		t.Fatalf("first request should be allowed: %+v", first)
	}

	second, err := window.Allow(ctx, key, 2, 60, 1)
	if err != nil {
		t.Fatalf("second Allow: %v", err)
	}
	if second.Allowed {
		t.Fatalf("second request should be rejected: %+v", second)
	}
}

func TestRedisClusterTokenBucketReservations(t *testing.T) {
	client := newRedisClusterIntegrationClient(t)
	ctx := context.Background()

	limiter := NewTokenBucketReservationLimiter(client)
	key := "cluster-reservation-" + time.Now().Format("150405.000000000")

	decision, reservation, err := limiter.Reserve(ctx, key, 5, 1, 3)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if !decision.Allowed {
		t.Fatalf("reservation should be allowed: %+v", decision)
	}
	if reservation == nil {
		t.Fatal("expected a reservation")
	}

	if err := reservation.Abort(ctx); err != nil {
		t.Fatalf("Abort: %v", err)
	}

	// Aborting should release the reserved quota.
	decision, reservation, err = limiter.Reserve(ctx, key, 5, 1, 5)
	if err != nil {
		t.Fatalf("Reserve after Abort: %v", err)
	}
	if !decision.Allowed {
		t.Fatalf("request should be allowed after Abort: %+v", decision)
	}
	if reservation == nil {
		t.Fatal("expected a second reservation")
	}

	if err := reservation.Commit(ctx); err != nil {
		t.Fatalf("Commit: %v", err)
	}
}

func TestRedisClusterKeysShareHashSlot(t *testing.T) {
	keys := tokenBucketKeysFor("cluster-slot-check")

	slot := redisSlotForTest(keys.State)

	for _, key := range []string{
		keys.Reservations,
		keys.ReservationData,
		keys.TerminalIndex,
	} {
		if got := redisSlotForTest(key); got != slot {
			t.Fatalf("keys do not share a Redis Cluster slot: %q=%d, expected %d",
				key, got, slot)
		}
	}

	prefix := redisClusterKeyPrefix("cluster-slot-check")
	slidingKeys := []string{
		prefix + ":swl:z",
		prefix + ":swl:cost",
		prefix + ":swl:usage",
		prefix + ":swl:seq",
	}

	for _, key := range slidingKeys {
		if got := redisSlotForTest(key); got != slot {
			t.Fatalf("sliding-window key %q has slot %d, expected %d",
				key, got, slot)
		}
	}
}

func redisSlotForTest(key string) uint16 {
	// Redis Cluster uses the substring inside {...} as the hash key.
	start := strings.IndexByte(key, '{')
	if start >= 0 {
		end := strings.IndexByte(key[start+1:], '}')
		if end > 0 {
			key = key[start+1 : start+1+end]
		}
	}

	var crc uint16

	for i := 0; i < len(key); i++ {
		crc ^= uint16(key[i]) << 8

		for bit := 0; bit < 8; bit++ {
			if crc&0x8000 != 0 {
				crc = (crc << 1) ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}

	return crc % 16384
}
