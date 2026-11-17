package limiter

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func newReservationRedisClient() *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr: "127.0.0.1:6379",
	})
}

func reservationTestKey(name string) string {
	return fmt.Sprintf(
		"test:token-bucket-reservation:%s:%d",
		name,
		time.Now().UnixNano(),
	)
}

func TestTokenBucketReservation_ReserveAndCommit(t *testing.T) {
	client := newReservationRedisClient()
	defer client.Close()

	ctx := context.Background()

	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis unavailable: %v", err)
	}

	limiter := NewTokenBucketReservationLimiter(client)
	key := reservationTestKey("commit")

	decision, reservation, err := limiter.Reserve(
		ctx,
		key,
		10,
		10,
		4,
	)
	if err != nil {
		t.Fatalf("reserve failed: %v", err)
	}

	if !decision.Allowed {
		t.Fatal("expected reservation to be allowed")
	}

	if reservation == nil {
		t.Fatal("expected reservation")
	}

	if decision.Remaining != 6 {
		t.Fatalf("expected 6 remaining, got %d", decision.Remaining)
	}

	if err := reservation.Commit(ctx); err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	if err := reservation.Commit(ctx); err != ErrReservationClosed {
		t.Fatalf("expected ErrReservationClosed, got %v", err)
	}
}

func TestTokenBucketReservation_AbortRestoresTokens(t *testing.T) {
	client := newReservationRedisClient()
	defer client.Close()

	ctx := context.Background()

	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis unavailable: %v", err)
	}

	limiter := NewTokenBucketReservationLimiter(client)
	key := reservationTestKey("abort")

	_, reservation, err := limiter.Reserve(
		ctx,
		key,
		10,
		10,
		4,
	)
	if err != nil {
		t.Fatalf("reserve failed: %v", err)
	}

	if reservation == nil {
		t.Fatal("expected reservation")
	}

	if err := reservation.Abort(ctx); err != nil {
		t.Fatalf("abort failed: %v", err)
	}

	decision, _, err := limiter.Reserve(
		ctx,
		key,
		10,
		10,
		10,
	)
	if err != nil {
		t.Fatalf("second reserve failed: %v", err)
	}

	if !decision.Allowed {
		t.Fatal("expected restored tokens to allow full reservation")
	}
}

func TestTokenBucketReservation_RejectsWhenInsufficient(t *testing.T) {
	client := newReservationRedisClient()
	defer client.Close()

	ctx := context.Background()

	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis unavailable: %v", err)
	}

	limiter := NewTokenBucketReservationLimiter(client)
	key := reservationTestKey("reject")

	decision, reservation, err := limiter.Reserve(
		ctx,
		key,
		5,
		1,
		6,
	)
	if err != nil {
		t.Fatalf("reserve failed: %v", err)
	}

	if decision.Allowed {
		t.Fatal("expected reservation to be rejected")
	}

	if reservation != nil {
		t.Fatal("expected no reservation for rejected request")
	}
}

func TestTokenBucketReservation_ExpiredReservationIsRecovered(t *testing.T) {
	client := newReservationRedisClient()
	defer client.Close()

	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis unavailable: %v", err)
	}

	limiter := NewTokenBucketReservationLimiter(client)
	key := reservationTestKey("expire-recovery")

	keys := tokenBucketKeysFor(key)
	indexKey := keys.Reservations

	// Create a real reservation, then move its expiry into the past atomically.
	_, reservation, err := limiter.Reserve(ctx, key, 10, 1, 4)
	if err != nil {
		t.Fatalf("reserve failed: %v", err)
	}
	if reservation == nil {
		t.Fatal("expected reservation")
	}

	// Set the indexed expiration to a timestamp in the past. The following
	// Reserve call must recover the reservation's deducted tokens.
	if err := client.ZAdd(ctx, indexKey, redis.Z{
		Score:  float64(time.Now().Add(-time.Second).UnixMilli()),
		Member: reservation.reservationID,
	}).Err(); err != nil {
		t.Fatalf("set expired score: %v", err)
	}

	if err := client.HSet(
		ctx,
		reservation.reservationDataKey,
		reservation.reservationID+":expires_at_ms",
		time.Now().Add(-time.Second).UnixMilli(),
	).Err(); err != nil {
		t.Fatalf("set expired metadata: %v", err)
	}

	decision, _, err := limiter.Reserve(ctx, key, 10, 1, 10)
	if err != nil {
		t.Fatalf("reserve after expiration failed: %v", err)
	}
	if !decision.Allowed {
		t.Fatalf("expected expired reservation to restore quota; got %+v", decision)
	}

	status, err := client.HGet(
		ctx,
		reservation.reservationDataKey,
		reservation.reservationID+":status",
	).Result()

	if err != nil {
		t.Fatalf("read expired reservation status: %v", err)
	}
	if status != "EXPIRED" {
		t.Fatalf("expected expired reservation status EXPIRED, got %q", status)
	}

	if score, err := client.ZScore(ctx, indexKey, reservation.reservationID).Result(); err != redis.Nil {
		t.Fatalf("expected expired reservation to be removed from index; score=%v err=%v", score, err)
	}
}

func TestTokenBucketReservation_AbortAfterExpirationDoesNotRestoreTwice(t *testing.T) {
	client := newReservationRedisClient()
	defer client.Close()

	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis unavailable: %v", err)
	}

	limiter := NewTokenBucketReservationLimiter(client)
	key := reservationTestKey("abort-after-expiration")

	_, reservation, err := limiter.Reserve(ctx, key, 10, 1, 4)
	if err != nil {
		t.Fatalf("reserve failed: %v", err)
	}
	if reservation == nil {
		t.Fatal("expected reservation")
	}

	indexKey := reservation.indexKey
	if err := client.ZAdd(ctx, indexKey, redis.Z{
		Score:  float64(time.Now().Add(-time.Second).UnixMilli()),
		Member: reservation.reservationID,
	}).Err(); err != nil {
		t.Fatalf("set expired score: %v", err)
	}
	if err := client.HSet(
		ctx,
		reservation.reservationDataKey,
		reservation.reservationID+":expires_at_ms",
		time.Now().Add(-time.Second).UnixMilli(),
	).Err(); err != nil {
		t.Fatalf("set expired metadata: %v", err)
	}

	err = reservation.Abort(ctx)
	if err != ErrReservationClosed {
		t.Fatalf("expected ErrReservationClosed, got %v", err)
	}

	status, err := client.HGet(
		ctx,
		reservation.reservationDataKey,
		reservation.reservationID+":status",
	).Result()

	if err != nil {
		t.Fatalf("read reservation status: %v", err)
	}

	if status != "EXPIRED" {
		t.Fatalf("expected EXPIRED status, got %q", status)
	}

	if _, err := client.ZScore(
		ctx,
		reservation.indexKey,
		reservation.reservationID,
	).Result(); !errors.Is(err, redis.Nil) {
		t.Fatalf("expected reservation to be removed from active index, got %v", err)
	}

	keys := tokenBucketKeysFor(key)

	values, err := client.HMGet(
		ctx,
		keys.State,
		"tokens",
		"reserved",
	).Result()

	if err != nil {
		t.Fatalf("read bucket state: %v", err)
	}

	if values[0] != "10000000" {
		t.Fatalf("expected 10000000 available tokens, got %v", values[0])
	}

	if values[1] != "0" {
		t.Fatalf("expected zero reserved tokens, got %v", values[1])
	}
}

func TestTokenBucketReservation_SharesQuotaWithAllow(t *testing.T) {
	client := newReservationRedisClient()
	defer client.Close()

	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis unavailable: %v", err)
	}

	key := reservationTestKey("shared-quota")

	reservationLimiter := NewTokenBucketReservationLimiter(client)
	regularLimiter, err := NewRedisTokenBucket(client)
	if err != nil {
		t.Fatalf("create regular limiter: %v", err)
	}

	_, reservation, err := reservationLimiter.Reserve(ctx, key, 10, 0.001, 4)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if reservation == nil {
		t.Fatal("expected reservation")
	}

	// Six tokens remain available. A cost of seven must be rejected.
	decision, err := regularLimiter.Allow(ctx, key, 10, 0.001, 7)
	if err != nil {
		t.Fatalf("ordinary Allow: %v", err)
	}
	if decision.Allowed {
		t.Fatal("ordinary request bypassed the outstanding reservation")
	}

	if err := reservation.Abort(ctx); err != nil {
		t.Fatalf("abort reservation: %v", err)
	}

	// After abort, the full capacity should be available again.
	decision, err = regularLimiter.Allow(ctx, key, 10, 0.001, 10)
	if err != nil {
		t.Fatalf("Allow after abort: %v", err)
	}
	if !decision.Allowed {
		t.Fatalf("expected full capacity after abort, got %+v", decision)
	}
}

func TestTokenBucketReservation_AbortCanBeCalledSuccessfully(t *testing.T) {
	client := newReservationRedisClient()
	defer client.Close()

	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis unavailable: %v", err)
	}

	limiter := NewTokenBucketReservationLimiter(client)
	_, reservation, err := limiter.Reserve(
		ctx,
		reservationTestKey("abort-success"),
		10,
		1,
		4,
	)
	if err != nil {
		t.Fatalf("reserve failed: %v", err)
	}
	if reservation == nil {
		t.Fatal("expected reservation")
	}

	if err := reservation.Abort(ctx); err != nil {
		t.Fatalf("abort failed: %v", err)
	}

	// A successfully aborted reservation must not be aborted again.
	if err := reservation.Abort(ctx); !errors.Is(err, ErrReservationClosed) {
		t.Fatalf("expected ErrReservationClosed on second abort, got: %v", err)
	}
}

func TestTokenBucketReservation_CommitAfterExpirationIsRejected(t *testing.T) {
	client := newReservationRedisClient()
	defer client.Close()

	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis unavailable: %v", err)
	}

	limiter := NewTokenBucketReservationLimiter(client)
	_, reservation, err := limiter.Reserve(ctx, reservationTestKey("commit-expired"), 10, 1, 4)
	if err != nil {
		t.Fatalf("reserve failed: %v", err)
	}
	if reservation == nil {
		t.Fatal("expected reservation")
	}

	expiredAt := time.Now().Add(-time.Second).UnixMilli()
	if err := client.ZAdd(ctx, reservation.indexKey, redis.Z{
		Score: float64(expiredAt), Member: reservation.reservationID,
	}).Err(); err != nil {
		t.Fatalf("expire index entry: %v", err)
	}
	if err := client.HSet(
		ctx,
		reservation.reservationDataKey,
		reservation.reservationID+":expires_at_ms",
		expiredAt,
	).Err(); err != nil {
		t.Fatalf("expire reservation metadata: %v", err)
	}

	// Verify that the active reservation has persistent bucket state.
	exists, err := client.Exists(ctx, reservation.key).Result()
	if err != nil {
		t.Fatalf("check bucket state before Commit: %v", err)
	}
	if exists != 1 {
		t.Fatalf("bucket state missing before Commit: %s", reservation.key)
	}

	ttl, err := client.PTTL(ctx, reservation.key).Result()
	if err != nil {
		t.Fatalf("read bucket TTL: %v", err)
	}
	if ttl >= 0 {
		t.Fatalf("expected persistent bucket state while reservation is active, got TTL %v", ttl)
	}

	keys := tokenBucketKeysFor(reservation.key)

	t.Logf("Reservation logical key: %q", reservation.key)
	t.Logf("Reservation bucket key: %q", reservation.key)
	t.Logf("Expected state key: %q", keys.State)
	t.Logf("Reservation data key: %q", reservation.reservationDataKey)
	t.Logf("Reservation active index: %q", reservation.indexKey)
	t.Logf("Reservation terminal index: %q", reservation.terminalIndexKey)

	statusBeforeCommit, err := client.HGet(
		ctx,
		reservation.reservationDataKey,
		reservation.reservationID+":status",
	).Result()
	if err != nil {
		t.Fatalf("read status before commit: %v", err)
	}

	stateBeforeCommit, err := client.HGetAll(
		ctx,
		reservation.key,
	).Result()
	if err != nil {
		t.Fatalf("read state before commit: %v", err)
	}

	t.Logf("Status before Commit: %s", statusBeforeCommit)
	t.Logf("State before Commit: %+v", stateBeforeCommit)

	if err := reservation.Commit(ctx); !errors.Is(err, ErrReservationClosed) {
		t.Fatalf("expected ErrReservationClosed, got %v", err)
	}

	statusAfterCommit, err := client.HGet(
		ctx,
		reservation.reservationDataKey,
		reservation.reservationID+":status",
	).Result()
	if err != nil {
		t.Fatalf("read status after commit: %v", err)
	}

	stateAfterCommit, err := client.HGetAll(
		ctx,
		reservation.key,
	).Result()
	if err != nil {
		t.Fatalf("read state after commit: %v", err)
	}

	t.Logf("Status after Commit: %s", statusAfterCommit)
	t.Logf("State after Commit: %+v", stateAfterCommit)

	status, err := client.HGet(
		ctx,
		reservation.reservationDataKey,
		reservation.reservationID+":status",
	).Result()

	if err != nil {
		t.Fatalf("read reservation status: %v", err)
	}

	if status != "EXPIRED" {
		t.Fatalf("expected EXPIRED status, got %q", status)
	}

	if _, err := client.ZScore(
		ctx,
		reservation.indexKey,
		reservation.reservationID,
	).Result(); !errors.Is(err, redis.Nil) {
		t.Fatalf("expected reservation to be removed from active index, got %v", err)
	}

	stateExists, err := client.Exists(ctx, reservation.key).Result()

	if err != nil {
		t.Fatalf("check bucket state existence: %v", err)
	}

	state, err := client.HGetAll(ctx, reservation.key).Result()
	if err != nil {
		t.Fatalf("read bucket state: %v", err)
	}

	t.Logf("Redis address: %s", client.Options().Addr)
	t.Logf("Bucket state key: %s", keys.State)
	t.Logf("Bucket state exists: %d", stateExists)
	t.Logf("Bucket state contents: %+v", state)

	reserved, err := client.HGet(ctx, reservation.key, "reserved").Int64()
	if err != nil {
		t.Fatalf("read reserved quota: %v", err)
	}

	if reserved != 0 {
		t.Fatalf("expected zero reserved quota, got %d", reserved)
	}
}

func TestTokenBucketReservation_AllowRecoversExpiredReservation(t *testing.T) {
	client := newReservationRedisClient()
	defer client.Close()

	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis unavailable: %v", err)
	}

	key := reservationTestKey("allow-expiry-recovery")
	reservationLimiter := NewTokenBucketReservationLimiter(client)

	regularLimiter, err := NewRedisTokenBucket(client)
	if err != nil {
		t.Fatalf("create regular limiter: %v", err)
	}

	_, reservation, err := reservationLimiter.Reserve(ctx, key, 10, 1, 4)
	if err != nil {
		t.Fatalf("reserve failed: %v", err)
	}
	if reservation == nil {
		t.Fatal("expected reservation")
	}

	// Simulate expiration without waiting for the production 30-second TTL.
	expiredAt := time.Now().Add(-time.Second).UnixMilli()

	if err := client.ZAdd(ctx, reservation.indexKey, redis.Z{
		Score:  float64(expiredAt),
		Member: reservation.reservationID,
	}).Err(); err != nil {
		t.Fatalf("expire index entry: %v", err)
	}

	if err := client.HSet(
		ctx,
		reservation.reservationDataKey,
		reservation.reservationID+":expires_at_ms",
		expiredAt,
	).Err(); err != nil {
		t.Fatalf("expire reservation metadata: %v", err)
	}

	// The expired reservation must no longer hold four tokens.
	decision, err := regularLimiter.Allow(ctx, key, 10, 1, 10)
	if err != nil {
		t.Fatalf("Allow after expiration: %v", err)
	}
	if !decision.Allowed {
		t.Fatalf("expected expired reservation to be recovered before Allow; got %+v", decision)
	}
}

func TestNextReservationID(t *testing.T) {
	const count = 10000

	ids := make(chan string, count)
	errs := make(chan error, count)

	var wg sync.WaitGroup

	for i := 0; i < count; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			id, err := nextReservationID()
			if err != nil {
				errs <- err
				return
			}

			ids <- id
		}()
	}

	wg.Wait()
	close(ids)
	close(errs)

	for err := range errs {
		t.Fatal(err)
	}

	seen := make(map[string]struct{}, count)

	for id := range ids {
		if len(id) != 32 {
			t.Fatalf("expected 32-character ID, got %q", id)
		}

		if _, err := hex.DecodeString(id); err != nil {
			t.Fatalf("ID is not hexadecimal: %q", id)
		}

		if _, exists := seen[id]; exists {
			t.Fatalf("duplicate reservation ID: %s", id)
		}

		seen[id] = struct{}{}
	}

	if len(seen) != count {
		t.Fatalf("expected %d unique IDs, got %d", count, len(seen))
	}
}

func TestReserveRejectsCapacityBeyondLuaPrecision(t *testing.T) {
	limiter := NewTokenBucketReservationLimiter(nil)

	_, reservation, err := limiter.Reserve(
		context.Background(),
		"precision-test",
		maxLuaExactInteger/reservationTokenScale+1,
		1,
		1,
	)

	if err == nil {
		t.Fatal("expected precision validation error")
	}

	if reservation != nil {
		t.Fatal("expected nil reservation")
	}
}
