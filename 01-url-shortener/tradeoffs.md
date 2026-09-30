# URL Shortener — Tradeoffs

This document records the major architectural decisions in the current URL shortener implementation, the alternatives considered, and why the current design was selected.

The goal is not to claim that one approach is universally better. Each decision is based on the current requirements, measured workload, implementation complexity, and expected evolution path.

---

## 1. Short-Code Generation

### Current choice

Use a PostgreSQL `BIGSERIAL` ID and encode the numeric ID using Base62.

```text
PostgreSQL ID → Base62 → short code
```

Example:

```text
ID: 1000
↓
Base62
↓
g8
```

### Alternative

Use randomly generated strings.

### Why the current design

The service currently has a relatively modest write rate compared with the available ID space.

The design target is approximately:

* 1M URL creations/day average
* ~11.6 creates/sec average
* ~60 creates/sec peak

A PostgreSQL sequence provides strong uniqueness without collision retries.

Base62 also produces compact identifiers.

For seven characters:

```text
62^7 ≈ 3.5 trillion
```

This is substantially larger than the expected 100M stored URLs.

### Tradeoff

The generated codes are predictable.

A user who knows one code may be able to infer nearby identifiers.

This is acceptable for the current system because enumeration resistance is not a V1 requirement.

If anti-enumeration becomes important, the code-generation strategy can be changed independently of the URL storage model.

---

## 2. PostgreSQL as the Source of Truth

### Current choice

PostgreSQL stores URL mappings and owns identifier generation.

### Alternative

Use a distributed key-value store or generate IDs independently using Snowflake-style identifiers.

### Why the current design

PostgreSQL provides:

* durable storage
* transactions
* uniqueness constraints
* indexing
* mature operational tooling
* straightforward relational modeling

The current workload does not justify the operational complexity of introducing distributed ID generation or a separate primary data store.

### Tradeoff

A single PostgreSQL deployment introduces a scaling and availability boundary.

The architecture can evolve toward:

```text
Primary + Read Replicas
```

or a distributed datastore if the workload eventually requires it.

---

## 3. Redis Cache-Aside

### Current choice

Use Redis as a cache in front of PostgreSQL.

Redirect resolution follows:

```text
Redis
  ↓ miss/error
PostgreSQL
  ↓
Redis
```

### Alternative

Perform every redirect lookup directly against PostgreSQL.

### Why the current design

Redirect traffic is expected to be substantially higher than URL creation traffic.

The design target is approximately:

* 100M redirects/day average
* ~1,157 redirects/sec average
* ~5.8K redirects/sec peak

Caching allows frequently accessed URLs to avoid database reads.

The measured hot-cache load test reached approximately:

* 4,298 requests/sec
* p50: ~10ms
* p95: ~21ms
* 0% errors

### Tradeoff

Redis becomes another infrastructure dependency.

The implementation therefore treats Redis as an optimization rather than the source of truth.

If Redis fails, the service falls back to PostgreSQL.

---

## 4. Redis Failure Behavior

### Current choice

A Redis read failure does not fail the redirect request.

The service attempts PostgreSQL.

A Redis write failure after a database lookup also does not fail the redirect.

### Alternative

Treat Redis as mandatory and return an error when Redis is unavailable.

### Why the current design

A cache should improve performance without unnecessarily becoming part of the correctness path.

The URL mapping exists in PostgreSQL, so the system can still resolve the URL when Redis is unavailable.

### Tradeoff

Database traffic increases during Redis failures.

This can increase database load during an infrastructure incident.

The design therefore preserves availability at the cost of degraded performance.

---

## 5. Expiration Uses `expires_at` as the Authority

### Current choice

PostgreSQL's `expires_at` value determines whether a URL is valid.

Redis TTL is not used as the business source of truth.

### Alternative

Rely entirely on Redis TTL.

### Why the current design

A cached URL and a valid URL are different concepts.

The cache may be evicted early, unavailable, or populated at a different point in time.

The business rule should therefore remain:

```text
now >= expires_at
→ expired
```

The same rule applies whether the request is served from Redis or PostgreSQL.

### Tradeoff

Every cache hit requires an expiration check against the cached metadata.

This adds negligible application-level work while making expiration semantics deterministic.

---

## 6. Custom Alias Uniqueness

### Current choice

PostgreSQL enforces uniqueness with a partial unique index:

```sql
CREATE UNIQUE INDEX idx_urls_custom_alias
ON urls(custom_alias)
WHERE custom_alias IS NOT NULL;
```

### Alternative

Check for an existing alias in application code before inserting.

### Why the current design

An application-only check is vulnerable to concurrent requests:

```text
Request A → alias available
Request B → alias available

Request A → insert
Request B → insert
```

The database constraint provides the final concurrency-safe guarantee.

The repository maps the PostgreSQL duplicate-key error to:

```text
ErrDuplicateAlias
```

which the HTTP layer returns as:

```text
409 Conflict
```

### Tradeoff

The application must understand and translate database constraint errors.

This is preferable to allowing uniqueness races.

---

## 7. Analytics in a Separate Table

### Current choice

Store analytics separately from the primary `urls` table.

```text
urls
url_analytics
```

The analytics table contains:

* redirect count
* last accessed timestamp

### Alternative

Store analytics columns directly on `urls`.

### Why the current design

Redirects are much more frequent than URL creation.

Updating the primary URL row for every redirect would make the hot URL record a write hotspot.

Separating analytics allows the primary mapping to remain focused on URL resolution.

### Tradeoff

Metadata requests require an additional analytics lookup.

The service handles the absence of an analytics row by returning:

```text
redirect_count = 0
last_accessed_at = null
```

---

## 8. In-Process Analytics Worker

### Current choice

Redirect events are emitted to a buffered, in-process worker.

The worker:

1. accepts redirect events non-blockingly
2. buffers events
3. flushes batches
4. writes them to PostgreSQL

Current defaults:

```text
Buffer size: 1000
Flush size: 100
Flush period: 1 second
```

### Alternative

Kafka or another durable event-streaming system.

### Why the current design

The current scale does not justify introducing Kafka solely for analytics.

The worker removes analytics database writes from the synchronous redirect path while keeping the implementation relatively small.

### Tradeoff

Events held only in process can be lost if the process crashes before they are persisted.

This is an explicit V1 durability tradeoff.

Kafka can be introduced later if analytics durability, replay, independent consumers, or higher event volume becomes important.

---

## 9. Dropping Analytics Events Under Backpressure

### Current choice

`Emit()` is non-blocking.

If the analytics buffer is full:

```text
redirect → succeeds
analytics event → dropped
```

Dropped events are counted by:

```text
url_analytics_events_dropped_total
```

### Alternative

Block the redirect request until analytics capacity becomes available.

### Why the current design

Analytics is secondary to URL resolution.

Blocking redirects because analytics storage is overloaded could directly affect the latency and availability of the primary user-facing operation.

### Tradeoff

Analytics becomes eventually consistent and can lose events under sustained overload.

This is acceptable because the current analytics requirement is basic counting rather than financially or operationally critical event accounting.

---

## 10. Analytics Repository Failure

### Current choice

Analytics persistence failure does not fail redirects.

The worker retains the failed batch and retries it.

### Alternative

Return an error to the redirect request.

### Why the current design

The redirect path should not depend on successful analytics persistence.

A temporary analytics database failure should degrade analytics freshness rather than make every redirect fail.

### Tradeoff

Analytics can become delayed while the worker retries.

If the failure persists and the process eventually shuts down or crashes, buffered events may be lost.

---

## 11. Database Connection Pool Size

### Current choice

The PostgreSQL pool uses:

```text
MaxConns = 10
MinConns = 2
```

### Alternative

Increase the pool substantially.

### Measurement

A pool increase to 25 connections was tested.

The larger pool did not produce a sufficiently meaningful improvement to justify changing the default.

The measured database-oriented benchmark with the current approach reached approximately:

```text
3,393 requests/sec
p95 ≈ 15ms
0% errors
```

### Why the current design

The benchmark indicates that simply increasing the connection pool is not the primary scaling solution.

A larger pool also increases the number of concurrent database connections and can create pressure on PostgreSQL.

### Tradeoff

The current pool can become a bottleneck as concurrency grows.

If future load testing demonstrates database connection contention, the pool should be revisited together with PostgreSQL capacity rather than increased blindly.

---

## 12. Cache Stampede Protection

### Current choice

No request coalescing or distributed locking is implemented.

### Alternative

Use:

* single-flight/request coalescing
* distributed locks
* stale-while-revalidate
* probabilistic early expiration

### Why the current design

The current implementation has not demonstrated a measured cache-stampede problem.

Adding synchronization mechanisms before observing the failure mode would increase complexity.

### Tradeoff

A popular key that expires or is evicted can cause many requests to query PostgreSQL simultaneously.

This should be addressed if production measurements demonstrate a meaningful stampede effect.

---

## 13. Metadata API Reads PostgreSQL

### Current choice

`GET /v1/urls/{code}` reads URL metadata from PostgreSQL and then reads analytics.

### Alternative

Serve metadata entirely from Redis.

### Why the current design

The metadata endpoint is an administrative/informational API rather than the latency-critical redirect path.

PostgreSQL remains the authoritative source for URL metadata.

Keeping this path simple avoids coupling metadata correctness to cache state.

### Tradeoff

Metadata requests generate database reads.

If metadata traffic becomes significant, a separate metadata cache can be introduced.

---

## 14. Generated Codes and Custom Aliases Share the Redirect Endpoint

### Current choice

Both generated codes and custom aliases use:

```text
GET /{code}
```

Resolution order:

```text
Redis
↓
custom alias
↓
Base62 decode
↓
numeric ID lookup
```

### Why the current design

This gives clients a single public redirect format.

Custom aliases can remain human-readable while generated identifiers remain compact.

### Tradeoff

The resolver needs two lookup strategies.

An alias that happens to resemble a valid Base62 identifier is resolved as an alias first, preserving custom-alias precedence.

---

## 15. Redis Cache Entry Contains Expiration Metadata

### Current choice

The cached value contains:

```text
URL ID
Original URL
Expires At
```

### Alternative

Cache only the original URL.

### Why the current design

Including `expires_at` allows the service to enforce expiration without querying PostgreSQL on every cache hit.

### Tradeoff

The cache contains slightly more data.

The benefit is that cached requests can independently determine whether the URL remains valid.

---

## 16. Current Single-Region Architecture

### Current choice

The system is designed as a horizontally scalable service within a single deployment/region.

### Alternative

Multi-region active-active architecture.

### Why the current design

Multi-region operation introduces additional complexity around:

* database replication
* consistency
* cache invalidation
* failover
* ID generation
* analytics aggregation
* operational deployment

The current requirements do not justify that complexity.

### Tradeoff

A regional outage can affect the service.

Multi-region deployment is an evolution path if availability requirements eventually exceed what a single region can provide.

---

## 17. No Kafka in V1

### Current choice

PostgreSQL + Redis + in-process analytics worker.

### Alternative

Kafka-based event architecture.

### Why the current design

Kafka becomes valuable when the system requires capabilities such as:

* durable event retention
* replay
* multiple independent consumers
* high event throughput
* consumer isolation
* asynchronous analytics pipelines

Those capabilities are not required by the current URL-shortener scope.

### Evolution path

A future architecture can change:

```text
API
 ↓
Kafka
 ├── Analytics consumer
 ├── Metrics consumer
 └── Other consumers
```

without fundamentally changing the public URL API.

---

## 18. Security and Configuration Defaults

### Current choice

Local development uses simple defaults such as:

```text
PostgreSQL: localhost:5432
Redis: localhost:6379
HTTP: localhost:8080
```

### Why the current design

These defaults make the system easy to run locally and are appropriate for an interview-preparation repository.

### Tradeoff

These values are not production deployment configuration.

Production deployments should provide:

* database credentials
* Redis configuration
* service URLs
* secrets
* environment-specific settings

through deployment configuration or a secret-management mechanism rather than relying on development defaults.

---

# Summary

The current architecture deliberately favors:

* PostgreSQL as the source of truth
* Redis as an optimization
* deterministic Base62 identifiers
* database-enforced uniqueness
* asynchronous best-effort analytics
* graceful degradation when non-critical dependencies fail
* measured rather than speculative optimization
* incremental evolution instead of premature distributed infrastructure

The major intentional V1 limitation is that analytics is not fully durable: events buffered in the API process can be lost during a process crash.

The architecture leaves clear evolution paths toward:

* Kafka
* read replicas
* stronger analytics durability
* cache stampede protection
* distributed ID generation
* multi-region deployment
* stronger anti-enumeration guarantees
