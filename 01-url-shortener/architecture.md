# URL Shortener — Architecture

## 1. Architectural Goals

The architecture is optimized for:

* high-volume URL redirects
* low redirect latency
* durable URL mappings
* horizontal API scaling
* strong uniqueness guarantees
* graceful degradation when the cache is unavailable

The primary design principle is:

> PostgreSQL is the source of truth. Redis is a performance optimization.

---

## 2. High-Level Architecture

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
          │ API #1 │        │ API #2 │        │ API #3 │
          └───┬────┘        └───┬────┘        └───┬────┘
              │                 │                 │
              └─────────────────┼─────────────────┘
                                │
                         ┌──────┴──────┐
                         │             │
                         ▼             ▼
                    ┌─────────┐  ┌────────────┐
                    │  Redis  │  │ PostgreSQL │
                    │  Cache  │  │   Source   │
                    └─────────┘  │  of Truth  │
                                 └──────┬─────┘
                                        │
                                        ▼
                                  ┌──────────┐
                                  │Analytics │
                                  │ Worker   │
                                  └──────────┘
```

---

## 3. Components

### 3.1 Load Balancer

Distributes requests across API instances.

API instances are stateless and can therefore scale horizontally.

---

### 3.2 API Service

Responsibilities:

* request validation
* URL creation
* short-code generation
* redirect resolution
* expiration validation
* cache interaction
* emitting redirect events

The API service does not own persistent state.

---

### 3.3 PostgreSQL

PostgreSQL is the authoritative datastore.

It stores:

* URL mappings
* expiration information
* creation metadata
* custom aliases

A database sequence generates unique numeric IDs.

---

### 3.4 Redis

Redis provides low-latency access to frequently requested URLs.

Redis is not authoritative.

If Redis loses its contents, mappings remain recoverable from PostgreSQL.

---

### 3.5 Analytics Worker

Redirect analytics are processed asynchronously rather than updating PostgreSQL synchronously on every redirect.

This prevents analytics writes from becoming a bottleneck on the latency-sensitive redirect path.

---

# 4. Data Model

```sql
CREATE TABLE urls (
    id BIGSERIAL PRIMARY KEY,

    original_url TEXT NOT NULL,

    custom_alias VARCHAR(64),

    expires_at TIMESTAMPTZ,

    redirect_count BIGINT NOT NULL DEFAULT 0,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    last_accessed_at TIMESTAMPTZ
);

CREATE UNIQUE INDEX idx_urls_custom_alias
ON urls(custom_alias)
WHERE custom_alias IS NOT NULL;
```

The generated short code is derived from `id` using Base62 encoding.

Example:

```text
1234567
   ↓
Base62
   ↓
5BAN
```

---

# 5. Short-Code Generation

Generated short codes use:

```text
Database sequence
        ↓
numeric ID
        ↓
Base62 encoding
        ↓
short code
```

Example:

```text
ID = 1234567
Code = 5BAN
```

This provides deterministic uniqueness without requiring random-code collision retries.

Custom aliases are stored separately and protected by a database uniqueness constraint.

---

# 6. Create URL Flow

Request:

```http
POST /v1/urls
```

### Flow

```text
Client
  │
  ▼
Load Balancer
  │
  ▼
API
  │
  ├── Validate URL
  │
  ├── Validate expiration
  │
  ├── Validate custom alias
  │
  ▼
PostgreSQL sequence
  │
  ▼
Generate numeric ID
  │
  ▼
Base62 encode
  │
  ▼
INSERT mapping
  │
  ▼
Return short URL
```

Redis is not required for URL creation.

---

# 7. Redirect Flow

Request:

```http
GET /5BAN
```

### Flow

```text
Client
  │
  ▼
Load Balancer
  │
  ▼
API
  │
  ▼
Decode Base62
  │
  ▼
Redis lookup
  │
  ├──────── HIT ──────────► URL
  │
  └──────── MISS
               │
               ▼
          PostgreSQL
               │
               ▼
             URL
               │
               ▼
          Populate Redis
```

The API then validates expiration and returns:

```http
302 Found
Location: https://example.com/...
```

---

# 8. Redis Cache

Cache key:

```text
url:{code}
```

Example:

```text
url:5BAN
```

Cached value:

```json
{
  "original_url": "https://example.com/products/123",
  "expires_at": null
}
```

Initial TTL:

```text
24 hours
```

The TTL is an optimization.

The authoritative expiration value is `expires_at`.

---

# 9. Cache-Aside Strategy

The API uses cache-aside behavior.

### Read

```text
1. Read Redis.
2. If hit, use cached value.
3. If miss, read PostgreSQL.
4. Populate Redis.
5. Return result.
```

### Write

```text
1. Write PostgreSQL.
2. Cache may be populated afterward.
```

Because PostgreSQL is authoritative, cache loss does not cause data loss.

---

# 10. Expiration

Every URL may have an optional `expires_at`.

For a redirect:

```text
current_time >= expires_at
```

results in:

```http
410 Gone
```

Otherwise:

```http
302 Found
```

Expiration must be checked using the authoritative expiration information rather than relying solely on Redis TTL.

---

# 11. Custom Aliases

Example:

```json
{
  "url": "https://example.com/docs",
  "custom_alias": "docs"
}
```

The database enforces uniqueness:

```sql
CREATE UNIQUE INDEX idx_urls_custom_alias
ON urls(custom_alias)
WHERE custom_alias IS NOT NULL;
```

This prevents two URLs from simultaneously owning the same alias.

---

# 12. Analytics

Analytics are deliberately removed from the synchronous redirect path.

Instead:

```text
Redirect
   │
   ├──────────► Client
   │
   ▼
Redirect Event
   │
   ▼
Analytics Worker
   │
   ▼
Aggregation / Storage
```

The initial implementation may use a lightweight asynchronous mechanism.

A dedicated distributed event-streaming system can be introduced in a later iteration.

---

# 13. Failure Scenarios

## Redis Failure

Behavior:

```text
Redis unavailable
      ↓
Read PostgreSQL
      ↓
Return redirect
```

Impact:

* increased latency
* increased database load

No URL mappings are lost.

---

## PostgreSQL Failure

Cached URLs may continue to redirect.

Cache misses cannot be resolved until PostgreSQL becomes available.

Future versions may introduce:

* PostgreSQL read replicas
* failover
* multi-region replication

---

## API Instance Failure

Because API instances are stateless, the load balancer can route requests to healthy instances.

No persistent application state is lost.

---

## Analytics Worker Failure

Redirects should continue functioning.

Events may remain queued until workers recover.

The redirect path must not depend synchronously on analytics processing.

---

# 14. Scaling Strategy

### API

Scale horizontally:

```text
API #1
API #2
API #3
...
API #N
```

because instances are stateless.

### Redis

Introduce Redis replication and/or clustering when required by workload.

### PostgreSQL

Initially:

```text
PostgreSQL Primary
```

As read traffic increases:

```text
             ┌──────────────┐
             │   Primary    │
             └──────┬───────┘
                    │
             ┌──────┴───────┐
             ▼              ▼
        Read Replica    Read Replica
```

Further scaling may require partitioning/sharding depending on workload.

---

# 15. Known Bottlenecks

The initial architecture has several deliberate limitations:

1. PostgreSQL participates in ID generation.
2. PostgreSQL is a potential single primary bottleneck.
3. Redis cache misses increase database load.
4. Hot-key traffic can concentrate on a single Redis key.
5. Analytics storage is not yet independently scalable.
6. Cache stampedes are not yet addressed.
7. Multi-region operation is not supported.

These are candidates for future iterations rather than premature complexity.

---

# 16. Evolution Plan

### Version 1

```text
Stateless API
     +
PostgreSQL
     +
Redis
```

### Version 2

Add:

```text
PostgreSQL read replicas
     +
async analytics processing
```

### Version 3

Add:

```text
distributed ID generation
     +
queue/event stream
     +
cache stampede protection
```

### Version 4

Explore:

```text
multi-region deployment
     +
regional caches
     +
replicated databases
```
