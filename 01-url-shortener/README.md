# URL Shortener

A production-oriented URL shortener implemented in Go as part of the `system-design-lab` system-design-lab repository.

The project focuses on understanding the architecture and tradeoffs behind a read-heavy distributed service rather than simply demonstrating framework usage.

## What This System Does

The service provides:

* Short URL creation
* HTTP/HTTPS URL validation
* Generated Base62 short codes
* Optional custom aliases
* URL expiration
* Redirect handling
* Redis caching
* Basic redirect analytics
* URL metadata retrieval
* Prometheus metrics
* Graceful shutdown
* Automated tests
* k6 load testing

The current implementation uses PostgreSQL as the source of truth and Redis as a performance optimization.

---

## Architecture

```text
Client
  |
  v
Load Balancer
  |
  v
+-----------------------------+
| Go API Instance             |
|                             |
|  POST /v1/urls              |
|  GET  /{code}               |
|  GET  /v1/urls/{code}       |
|  GET  /health               |
|  GET  /metrics              |
+-------------+---------------+
              |
        +-----+-----+
        |           |
        v           v
      Redis     PostgreSQL
        |           |
        |           +----------------+
        |                            |
        |                  url_analytics
        |                            ^
        |                            |
        +---- cache-aside -----------+
                                     |
                              Analytics Worker
                              (in-process)
```

For detailed architecture decisions, see [`architecture.md`](architecture.md).

For the decision rationale and alternatives considered, see [`tradeoffs.md`](tradeoffs.md).

---

## Core Request Flows

### Create URL

```text
Client
  |
  | POST /v1/urls
  v
HTTP Handler
  |
  v
URL Service
  |
  +--> Validate URL / expiration / alias
  |
  v
PostgreSQL
  |
  | BIGSERIAL ID
  v
Base62 encoding
  |
  v
Short URL response
```

### Redirect

```text
Client
  |
  | GET /{code}
  v
URL Handler
  |
  v
URL Service
  |
  v
Redis
  |
  +--> HIT --> expiration check --> 302
  |
  +--> MISS / ERROR
           |
           v
      PostgreSQL
           |
           v
        Redis SET
           |
           v
          302
```

Analytics is emitted asynchronously and does not block the redirect.

---

## Technology Stack

| Component            | Technology                     |
| -------------------- | ------------------------------ |
| Language             | Go                             |
| HTTP server          | Go `net/http`                  |
| Primary database     | PostgreSQL                     |
| Cache                | Redis                          |
| Analytics            | PostgreSQL + in-process worker |
| Metrics              | Prometheus                     |
| Load testing         | k6                             |
| Local infrastructure | Docker Compose                 |

---

## Repository Structure

```text
01-url-shortener/
├── README.md
├── requirements.md
├── architecture.md
├── tradeoffs.md
├── diagrams/
│   ├── system-architecture.mmd
│   ├── create-url-flow.mmd
│   ├── redirect-flow.mmd
│   └── analytics-flow.mmd
└── implementation/
    ├── README.md
    ├── cmd/
    ├── internal/
    ├── migrations/
    └── tests/
        └── load/
```

---

## API

### Create URL

```http
POST /v1/urls
Content-Type: application/json
```

Request:

```json
{
  "url": "https://example.com/very/long/path"
}
```

Optional fields:

```json
{
  "url": "https://example.com/very/long/path",
  "custom_alias": "docs",
  "expires_at": "2027-01-01T00:00:00Z"
}
```

Successful response:

```json
{
  "code": "B",
  "short_url": "http://localhost:8080/B",
  "original_url": "https://example.com/very/long/path"
}
```

### Redirect

```http
GET /{code}
```

Successful resolution returns:

```http
302 Found
Location: https://example.com/very/long/path
```

Possible responses:

| Status | Meaning                   |
| ------ | ------------------------- |
| `302`  | URL resolved              |
| `404`  | Short code does not exist |
| `410`  | URL has expired           |
| `500`  | Unexpected server error   |

### Metadata

```http
GET /v1/urls/{code}
```

Example:

```json
{
  "code": "B",
  "original_url": "https://example.com/very/long/path",
  "created_at": "2026-09-30T08:00:00Z",
  "expires_at": null,
  "redirect_count": 2,
  "last_accessed_at": "2026-09-30T08:01:00Z"
}
```

### Health

```http
GET /health
```

Used for basic service health checks.

### Metrics

```http
GET /metrics
```

Exposes Prometheus metrics.

---

## Data Model

### `urls`

```text
id              BIGSERIAL PRIMARY KEY
original_url    TEXT NOT NULL
custom_alias    VARCHAR(64)
expires_at      TIMESTAMPTZ
created_at      TIMESTAMPTZ NOT NULL
```

A partial unique index guarantees custom-alias uniqueness.

### `url_analytics`

```text
url_id              BIGINT PRIMARY KEY
redirect_count      BIGINT NOT NULL
last_accessed_at    TIMESTAMPTZ
```

Analytics is intentionally separated from the primary URL record so frequent redirect updates do not modify the main URL row.

---

## Short-Code Generation

Generated codes use:

```text
PostgreSQL BIGSERIAL
        |
        v
    Base62 Encode
        |
        v
    Short Code
```

Base62 provides:

```text
62^7 ≈ 3.5 trillion
```

possible seven-character combinations.

The current design uses database-generated IDs instead of random strings, avoiding collision retries and distributed ID-generation infrastructure.

---

## Caching

Redis uses a cache-aside strategy.

Cache key:

```text
url:{code}
```

Cached value contains:

```json
{
  "url_id": 123,
  "original_url": "https://example.com",
  "expires_at": null
}
```

Redis failures do not fail redirects.

The service falls back to PostgreSQL.

Expiration is determined by `expires_at`, not Redis TTL alone.

---

## Analytics

Redirect events are emitted to an in-process buffered analytics worker.

Current defaults:

```text
Buffer size: 1000
Batch size: 100
Flush interval: 1 second
```

The worker writes aggregated redirect information to PostgreSQL.

Analytics is deliberately outside the synchronous redirect critical path.

If the analytics buffer becomes full, events may be dropped rather than blocking redirects.

Dropped events are exposed through:

```text
url_analytics_events_dropped_total
```

A process crash before buffered events are persisted can result in analytics loss. This is an explicit V1 tradeoff.

---

## Observability

The service exposes Prometheus metrics including:

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

These metrics provide visibility into:

* request volume
* request latency
* cache effectiveness
* database lookup volume
* redirects
* URL creation
* analytics backpressure

---

## Performance Evidence

The implementation was tested with k6 rather than relying only on theoretical capacity estimates.

### Hot-cache redirect test

Configuration:

```text
50 VUs
30 seconds
```

Measured:

| Metric     |        Result |
| ---------- | ------------: |
| Requests   |       128,996 |
| Throughput | 4,297.9 req/s |
| Average    |      11.17 ms |
| p50        |      10.03 ms |
| p90        |      17.45 ms |
| p95        |      20.81 ms |
| Maximum    |     205.52 ms |
| Error rate |            0% |

### Database-oriented test

A separate temporary benchmark exercised the PostgreSQL lookup path against approximately 1M URL rows.

Measured:

| Metric     |        Result |
| ---------- | ------------: |
| Requests   |       101,800 |
| Throughput | 3,392.8 req/s |
| Average    |         ~7 ms |
| p50        |       5.86 ms |
| p90        |      12.19 ms |
| p95        |      15.01 ms |
| Maximum    |     131.24 ms |
| Error rate |            0% |

These are measurements of the development environment and should not be interpreted as production capacity guarantees.

---

## Reliability Behavior

### Redis unavailable

```text
Redis GET fails
      |
      v
PostgreSQL lookup
      |
      v
Redirect continues
```

### Analytics unavailable

```text
Analytics persistence fails
      |
      v
Worker retries
      |
      v
Redirect remains unaffected
```

### Expired URL

Expiration is checked regardless of whether the URL came from Redis or PostgreSQL.

```text
now >= expires_at
        |
        v
    HTTP 410
```

### Graceful shutdown

Shutdown sequence:

```text
Shutdown signal
      |
      v
Stop accepting HTTP work
      |
      v
Stop analytics worker
      |
      v
Flush accepted analytics events
      |
      v
Close Redis/PostgreSQL
```

The worker has a bounded shutdown window to avoid hanging indefinitely.

---

## Testing

The implementation contains unit and integration-oriented tests for:

* Base62 encoding/decoding
* URL validation
* URL creation
* duplicate aliases
* URL resolution
* expiration
* Redis failure handling
* analytics failure isolation
* analytics worker batching
* analytics retry behavior
* analytics event dropping
* metadata retrieval
* Prometheus metrics

Run the complete Go test suite:

```powershell
go test ./...
```

On Windows environments where Application Control blocks temporary Go test executables, individual packages can be compiled explicitly:

```powershell
go test -c -o handler.test.exe ./internal/handler
.\handler.test.exe
Remove-Item .\handler.test.exe
```

---

## Local Development

Start PostgreSQL and Redis:

```powershell
docker compose up -d
```

Run the service:

```powershell
go run ./cmd/server
```

The default development server listens on:

```text
http://localhost:8080
```

PostgreSQL:

```text
localhost:5432
```

Redis:

```text
localhost:6379
```

The application configuration provides local development defaults. Production deployments should provide environment-specific configuration and secrets through deployment configuration rather than relying on these defaults.

---

## Load Testing

The persistent load-test script is:

```text
implementation/tests/load/redirect.js
```

The repository intentionally does not retain temporary diagnostic or benchmark endpoints used during performance investigation.

Performance testing should exercise the real public HTTP paths rather than bypassing the application.

---

## Design Scope

### Included

* URL shortening
* Redirects
* Custom aliases
* Expiration
* Redis caching
* Basic analytics
* Metadata API
* Prometheus metrics
* Horizontal API scaling
* Graceful shutdown

### Not included

* Authentication
* User accounts
* Teams and organizations
* Billing
* Custom domains
* QR codes
* Geo/device/referrer analytics
* Malicious URL scanning
* Advanced analytics dashboards
* Multi-region deployment
* Kafka-based event streaming

These capabilities can be introduced as later system-design exercises.

---

## Future Evolution

Potential evolution paths include:

```text
Current
PostgreSQL + Redis + in-process worker
             |
             +--> Read replicas
             |
             +--> Kafka analytics pipeline
             |
             +--> Distributed ID generation
             |
             +--> Cache stampede protection
             |
             +--> Multi-region deployment
             |
             +--> Stronger anti-enumeration
```

Each evolution should be driven by a demonstrated requirement or measured bottleneck rather than introduced prematurely.

---

## Related Documentation

* [`requirements.md`](requirements.md) — functional and non-functional requirements
* [`architecture.md`](architecture.md) — detailed architecture
* [`tradeoffs.md`](tradeoffs.md) — architectural decisions and alternatives
* [`implementation/README.md`](implementation/README.md) — implementation guide
* [`diagrams/`](diagrams/) — Mermaid architecture and flow diagrams
