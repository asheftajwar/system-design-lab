# URL Shortener — Architecture

## 1. Architectural Goals

The URL shortener is designed around a read-heavy workload where redirect latency is more important than write throughput.

The architecture prioritizes:

* low-latency redirects
* durable URL mappings
* deterministic short-code generation
* horizontal API scalability
* strong uniqueness for custom aliases
* graceful degradation when Redis is unavailable
* asynchronous, non-blocking redirect analytics
* explicit observability through Prometheus metrics

The primary design principle is:

> **PostgreSQL is the source of truth. Redis is a performance optimization.**

The current implementation is intentionally a single-region, single-PostgreSQL-primary system. Kafka, Kubernetes, multi-region replication, distributed ID generation, and advanced analytics are not part of the current implementation.

---

# 2. Current High-Level Architecture

```text
                         ┌─────────────┐
                         │   Client    │
                         └──────┬──────┘
                                │
                                ▼
                         ┌─────────────┐
                         │Load Balancer│
                         └──────┬──────┘
                                │
              ┌─────────────────┼─────────────────┐
              │                 │                 │
              ▼                 ▼                 ▼
          ┌────────┐        ┌────────┐        ┌────────┐
          │ API #1 │        │ API #2 │        │ API #N │
          └───┬────┘        └───┬────┘        └───┬────┘
              │                 │                 │
              └─────────────────┼─────────────────┘
                                │
                     ┌──────────┴──────────┐
                     │                     │
                     ▼                     ▼
                ┌─────────┐         ┌────────────┐
                │  Redis  │         │ PostgreSQL │
                │  Cache  │         │   Source   │
                └─────────┘         │  of Truth  │
                                    └──────┬─────┘
                                           │
                                           ▼
                                  ┌────────────────┐
                                  │ Analytics      │
                                  │ Worker         │
                                  │                │
                                  │ buffered       │
                                  │ asynchronous   │
                                  └───────┬────────┘
                                          │
                                          ▼
                                  ┌────────────────┐
                                  │ url_analytics  │
                                  └────────────────┘
```

The Go API instances are stateless. Persistent state is stored in PostgreSQL, while Redis stores derived cache entries.

The analytics worker runs inside the application process in the current implementation.

---

# 3. Components

## 3.1 Load Balancer

The target production architecture places multiple stateless API instances behind a load balancer.

Because API instances do not own persistent application state, requests can be distributed across instances.

The current local implementation runs a single API process, but the application architecture does not require per-instance state.

---

## 3.2 Go API Service

The API service is responsible for:

* request validation
* URL creation
* custom alias validation
* short-code generation
* redirect resolution
* expiration checking
* Redis cache interaction
* metadata retrieval
* emitting redirect analytics events
* HTTP metrics

The API does not treat Redis as authoritative storage.

---

## 3.3 PostgreSQL

PostgreSQL is the authoritative datastore.

It stores:

* original URLs
* generated numeric IDs
* custom aliases
* expiration timestamps
* creation timestamps
* aggregated redirect analytics

A PostgreSQL `BIGSERIAL` primary key supplies the numeric ID used to generate deterministic Base62 short codes.

PostgreSQL therefore provides the uniqueness guarantee for generated IDs, while a partial unique index provides uniqueness for custom aliases.

---

## 3.4 Redis

Redis provides low-latency access to frequently requested URL mappings.

Redis is a cache, not a source of truth.

If Redis loses its contents, the application can reconstruct cache entries from PostgreSQL.

The current cache uses a 24-hour TTL, but URL expiration is independently checked using the cached `expires_at` value.

---

## 3.5 Analytics Worker

Redirect analytics are emitted asynchronously.

The redirect path does not synchronously update PostgreSQL analytics.

The worker:

1. receives accepted redirect events through a buffered channel
2. accumulates events into a batch
3. flushes when the batch reaches its configured size
4. flushes periodically
5. retries a failed batch on a later flush
6. flushes accepted events during graceful shutdown

Current defaults:

```text
Buffer size:       1000 events
Flush size:         100 events
Flush interval:     1 second
```

The worker is deliberately non-blocking.

If the buffer is full, the redirect request continues and the analytics event is dropped. Dropped events are counted by:

```text
url_analytics_events_dropped_total
```

This is an explicit V1 tradeoff: redirect availability and latency take priority over perfect analytics delivery.

---

# 4. Data Model

## 4.1 URL Mapping

```sql
CREATE TABLE urls (
    id BIGSERIAL PRIMARY KEY,
    original_url TEXT NOT NULL,
    custom_alias VARCHAR(64),
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX idx_urls_custom_alias
ON urls(custom_alias)
WHERE custom_alias IS NOT NULL;

CREATE INDEX idx_urls_expires_at
ON urls(expires_at)
WHERE expires_at IS NOT NULL;
```

The `urls` table does **not** contain redirect counters.

---

## 4.2 Analytics

Redirect statistics are stored separately:

```sql
CREATE TABLE url_analytics (
    url_id BIGINT PRIMARY KEY REFERENCES urls(id) ON DELETE CASCADE,
    redirect_count BIGINT NOT NULL DEFAULT 0,
    last_accessed_at TIMESTAMPTZ
);
```

This separation keeps redirect aggregation independent from the core URL mapping row.

A URL does not require an analytics row to exist. If no analytics record exists, metadata returns:

```text
redirect_count = 0
last_accessed_at = null
```

---

# 5. Short-Code Generation

Generated short codes use deterministic ID-to-Base62 encoding:

```text
PostgreSQL BIGSERIAL
        │
        ▼
    numeric ID
        │
        ▼
    Base62 encode
        │
        ▼
    short code
```

For example:

```text
ID
1234567

   ↓

Base62

   ↓

short code
```

The exact code length is determined by the numeric ID.

With seven Base62 characters, the theoretical namespace is:

```text
62^7 ≈ 3.5 trillion
```

The current system does not use random code generation, so it does not require collision retries.

Custom aliases bypass Base62 generation and are protected by the PostgreSQL unique partial index.

---

# 6. Create URL Flow

Endpoint:

```http
POST /v1/urls
```

Example request:

```json
{
  "url": "https://example.com/very/long/path",
  "expires_at": null,
  "custom_alias": null
}
```

Flow:

```text
Client
  │
  ▼
API
  │
  ├── Limit request body
  │
  ├── Parse JSON
  │
  ├── Validate URL
  │
  ├── Validate expiration
  │
  ├── Validate custom alias
  │
  ▼
PostgreSQL
  │
  ├── Allocate BIGSERIAL ID
  │
  └── Insert URL mapping
  │
  ▼
Base62 encode ID
  │
  ▼
Return short URL
```

Redis is not required to create a URL.

If a custom alias is supplied, the alias is persisted directly and uniqueness is enforced by PostgreSQL.

---

# 7. Redirect Flow

Endpoint:

```http
GET /{code}
```

The current implementation intentionally checks Redis before PostgreSQL.

```text
Client
  │
  ▼
API
  │
  ▼
Redis GET url:{code}
  │
  ├──────── HIT ───────────────┐
  │                            │
  │                            ▼
  │                     Decode cache entry
  │                            │
  │                            ▼
  │                     Check expiration
  │                            │
  │                            ▼
  │                     Emit analytics
  │                            │
  │                            ▼
  │                         302
  │
  └──────── MISS / ERROR
               │
               ▼
       PostgreSQL alias lookup
               │
          ┌────┴────┐
          │         │
        FOUND      NOT FOUND
          │         │
          │         ▼
          │    Base62 decode
          │         │
          │         ▼
          │    PostgreSQL ID lookup
          │         │
          └────┬────┘
               │
               ▼
        Check expiration
               │
               ▼
        Populate Redis
               │
               ▼
        Emit analytics
               │
               ▼
              302
```

Custom aliases take precedence during the database fallback path.

The API returns:

```http
HTTP/1.1 302 Found
Location: https://example.com/...
```

For an unknown code:

```http
404 Not Found
```

For an expired URL:

```http
410 Gone
```

---

# 8. Cache Entry

Cache key:

```text
url:{code}
```

Example:

```text
url:5BAN
```

The cached value is JSON:

```json
{
  "url_id": 1234567,
  "original_url": "https://example.com/products/123",
  "expires_at": null
}
```

The URL ID is cached because the redirect event needs to identify the URL for analytics.

The Redis TTL is currently:

```text
24 hours
```

The TTL controls cache retention only.

It is not the source of expiration truth.

---

# 9. Cache-Aside Strategy

The redirect path follows a cache-aside strategy.

## Cache hit

```text
1. Read Redis.
2. Decode cached URL entry.
3. Check expires_at.
4. Redirect.
5. Emit analytics event.
```

## Cache miss

```text
1. Read Redis.
2. Redis miss.
3. Look up custom alias.
4. If not found, decode Base62.
5. Look up numeric ID.
6. Check expires_at.
7. Populate Redis.
8. Redirect.
9. Emit analytics event.
```

## Redis failure

A Redis read or write failure does not fail a redirect when PostgreSQL can resolve the URL.

This gives the system graceful degradation at the expense of increased database traffic and latency.

---

# 10. Expiration

Expiration is represented by:

```text
expires_at
```

The application compares the current time with the authoritative expiration value.

If:

```text
current_time >= expires_at
```

the URL is considered expired.

The redirect endpoint returns:

```http
410 Gone
```

Expiration is checked for both:

* PostgreSQL results
* Redis cache hits

Therefore, a stale Redis TTL cannot make an expired URL valid.

Metadata for an expired URL remains retrievable through:

```http
GET /v1/urls/{code}
```

The metadata endpoint reports the URL and its expiration state rather than treating expiration as deletion.

---

# 11. Custom Aliases

Custom aliases are validated before persistence.

Allowed characters:

```text
A-Z
a-z
0-9
-
_
```

Length:

```text
1–64 characters
```

The database enforces uniqueness:

```sql
CREATE UNIQUE INDEX idx_urls_custom_alias
ON urls(custom_alias)
WHERE custom_alias IS NOT NULL;
```

If two requests attempt to create the same alias concurrently, PostgreSQL provides the final uniqueness guarantee.

The API maps the duplicate constraint violation to:

```http
409 Conflict
```

---

# 12. Metadata API

Endpoint:

```http
GET /v1/urls/{code}
```

Example response:

```json
{
  "code": "aB91x",
  "original_url": "https://example.com/very/long/path",
  "created_at": "2026-09-25T00:00:00Z",
  "expires_at": null,
  "redirect_count": 1542,
  "last_accessed_at": "2026-09-25T02:30:00Z"
}
```

The endpoint:

1. resolves custom aliases first
2. falls back to Base62 ID resolution
3. reads analytics separately
4. returns zero/null analytics when no analytics row exists

Analytics read failures are returned as API errors because this endpoint explicitly requests analytics data.

This is different from redirects, where analytics are best-effort.

---

# 13. Analytics Flow

The redirect request does not synchronously write analytics:

```text
                 ┌───────────────┐
                 │ Redirect      │
                 │ request       │
                 └───────┬───────┘
                         │
              ┌──────────┴──────────┐
              │                     │
              ▼                     ▼
          HTTP 302              Emit event
                                    │
                                    ▼
                              Buffered channel
                                    │
                                    ▼
                             Analytics Worker
                                    │
                            ┌───────┴───────┐
                            │               │
                       batch size       timer tick
                            │               │
                            └───────┬───────┘
                                    ▼
                              PostgreSQL
                                    │
                                    ▼
                             url_analytics
```

The database operation uses an upsert:

```text
new URL:
    redirect_count = 1

existing URL:
    redirect_count = redirect_count + 1
    last_accessed_at = event timestamp
```

---

# 14. Analytics Reliability Semantics

The worker has three important behaviors.

### Repository failure

A failed batch is retained and retried during a later flush.

Therefore, a transient database error does not immediately discard the batch.

### Buffer full

`Emit` is non-blocking.

If the buffer is full:

```text
event → dropped
redirect → continues
```

The drop is counted by:

```text
url_analytics_events_dropped_total
```

### Process crash

Events that have been accepted into memory but not successfully flushed to PostgreSQL can be lost if the process crashes.

This is a deliberate V1 limitation.

A durable queue such as Kafka could remove this limitation in a future architecture.

---

# 15. Failure Scenarios

## Redis Failure

```text
Redis unavailable
      │
      ▼
PostgreSQL fallback
      │
      ▼
Redirect
```

Impact:

* increased latency
* increased PostgreSQL load
* reduced cache effectiveness

URL mappings are not lost.

If the database is also unavailable, uncached redirects cannot be resolved.

---

## PostgreSQL Failure

A cached URL can continue to redirect if Redis contains a valid cache entry.

An uncached URL cannot be resolved without PostgreSQL.

Metadata requests that require analytics storage also depend on PostgreSQL.

---

## Analytics Failure

Analytics are intentionally outside the synchronous redirect critical path.

A failure to enqueue or persist analytics must not prevent the redirect response.

The worker retries failed database batches, while buffer saturation results in observable event drops.

---

## API Instance Failure

API instances are stateless.

With multiple instances behind a load balancer:

```text
Failed API instance
       │
       ▼
Load balancer
       │
       ▼
Healthy API instance
```

Persistent URL mappings remain in PostgreSQL.

---

# 16. Observability

The service exposes Prometheus metrics through:

```http
GET /metrics
```

Current metrics include:

```text
url_http_requests_total
url_http_request_duration_seconds

url_cache_hits_total
url_cache_misses_total
url_cache_errors_total

url_db_lookups_total
url_redirects_total
url_creations_total

url_analytics_events_dropped_total
```

The HTTP metrics use route patterns rather than individual dynamic URL values, preventing high-cardinality labels such as every individual short code.

Example:

```text
GET /{code}
```

rather than:

```text
GET /aB91x
GET /5BAN
GET /docs
...
```

---

# 17. Database Connection Pool

The current PostgreSQL pool configuration is:

```text
MaxConns:          10
MinConns:           2
MaxConnLifetime:    1 hour
```

Load testing showed that increasing the pool to 25 connections did not produce a proportional improvement in the tested workload.

The current configuration therefore remains deliberately conservative rather than treating a larger pool as an automatic performance improvement.

---

# 18. Measured Performance

The implementation was tested with k6 against the running service.

These measurements are benchmark observations, not guarantees of production capacity.

## Hot-cache redirect benchmark

Configuration:

```text
Virtual users: 50
Duration:      30 seconds
```

Observed:

```text
Requests:       128,996
Throughput:     ~4,298 req/s
Average:        11.17 ms
p50:            10.03 ms
p90:            17.45 ms
p95:            20.81 ms
Maximum:        205.52 ms
Errors:         0%
```

---

## Database-path benchmark

A separate temporary benchmark exercised the PostgreSQL lookup path against approximately one million URL rows.

Observed:

```text
Requests:       101,800
Throughput:     ~3,393 req/s
Average:        ~7 ms
p50:            5.86 ms
p90:            12.19 ms
p95:            15.01 ms
Maximum:        131.24 ms
Errors:         0%
```

The temporary benchmark-only server endpoints used during investigation were removed afterward.

The permanent load-test script remains under:

```text
tests/load/redirect.js
```

---

# 19. Known Bottlenecks and Deliberate Limitations

The current implementation has several known limitations:

1. PostgreSQL supplies generated IDs.
2. PostgreSQL currently has a single primary.
3. Redis misses increase PostgreSQL load.
4. Redis hot keys may concentrate traffic.
5. Analytics buffering is process-local.
6. Analytics events can be dropped when the worker buffer is full.
7. Unflushed analytics can be lost during process crashes.
8. Cache stampede protection is not implemented.
9. There is no durable event stream.
10. There is no multi-region deployment.
11. There are no PostgreSQL read replicas.
12. There is no distributed ID generator.
13. There is no advanced analytics pipeline.

These are deliberate V1 boundaries, not missing prerequisites for the current system.

---

# 20. Evolution Strategy

## Current V1

```text
Stateless Go API
       +
PostgreSQL
       +
Redis
       +
In-process analytics worker
       +
Prometheus metrics
```

## Possible V2

If measurements justify additional database read capacity:

```text
PostgreSQL Primary
       │
       ├── Read Replica
       └── Read Replica
```

If analytics durability becomes important:

```text
API
 │
 ▼
Durable Event Stream
 │
 ▼
Analytics Consumers
 │
 ▼
Analytics Storage
```

Kafka is a candidate for this role, but it is intentionally not part of the current implementation.

## Possible V3

Depending on measured requirements:

* distributed ID generation
* cache stampede protection
* Redis replication/cluster
* dedicated analytics storage
* PostgreSQL partitioning or sharding

## Possible V4

For a genuinely global workload:

* regional API deployments
* regional caches
* replicated databases
* multi-region failover
* conflict/consistency strategy

These should be introduced in response to concrete workload or availability requirements rather than added to the current V1 design prematurely.
