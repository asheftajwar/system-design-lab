# URL Shortener — Implementation Guide

## 1. Purpose

This directory contains the working Go implementation of the URL-shortener system described by the parent architecture and requirements documents.

The implementation is intentionally small and production-oriented enough to demonstrate the core system-design decisions without introducing infrastructure that the current workload does not require.

Current stack:

```text
Go
PostgreSQL
Redis
Prometheus
Docker Compose
k6
```

---

# 2. Directory Structure

```text
implementation/
├── cmd/
│   └── server/
│       └── main.go
│
├── internal/
│   ├── analytics/
│   │   ├── worker.go
│   │   └── worker_test.go
│   │
│   ├── base62/
│   │   ├── base62.go
│   │   └── base62_test.go
│   │
│   ├── cache/
│   │   ├── cache.go
│   │   └── redis.go
│   │
│   ├── config/
│   │   └── config.go
│   │
│   ├── domain/
│   │   └── url.go
│   │
│   ├── handler/
│   │   ├── url_handler.go
│   │   └── url_handler_test.go
│   │
│   ├── metrics/
│   │   ├── metrics.go
│   │   └── metrics_test.go
│   │
│   ├── repository/
│   │   ├── analytics_repository.go
│   │   ├── postgres_analytics_repository.go
│   │   ├── postgres_url_repository.go
│   │   ├── url_repository.go
│   │   └── ...
│   │
│   └── service/
│       ├── url_service.go
│       └── url_service_test.go
│
├── migrations/
│   ├── 001_create_urls.sql
│   └── 002_create_url_analytics.sql
│
├── tests/
│   └── load/
│       └── redirect.js
│
├── Dockerfile
├── docker-compose.yml
├── go.mod
└── go.sum
```

---

# 3. Application Layers

The implementation follows a simple layered structure.

```text
HTTP Handler
     │
     ▼
Service
     │
     ├──────────────► Cache
     │
     ├──────────────► URL Repository
     │
     └──────────────► Analytics Emitter
                            │
                            ▼
                     Analytics Worker
                            │
                            ▼
                    Analytics Repository
                            │
                            ▼
                       PostgreSQL
```

The layers have distinct responsibilities.

---

# 4. Handler Layer

Location:

```text
internal/handler/
```

The handler layer is responsible for HTTP concerns:

* JSON decoding
* request-size limits
* HTTP status codes
* response serialization
* mapping service errors to HTTP responses
* extracting `{code}` path values

Routes:

```text
GET  /health
GET  /metrics
POST /v1/urls
GET  /{code}
GET  /v1/urls/{code}
```

The redirect handler returns `302 Found`.

Expired URLs return `410 Gone`.

Unknown URLs return `404 Not Found`.

---

# 5. Service Layer

Location:

```text
internal/service/
```

The service layer contains the URL-shortening business logic.

Responsibilities include:

* URL validation
* expiration validation
* alias validation
* URL creation
* Base62 generation
* redirect resolution
* cache-aside behavior
* expiration checks
* metadata retrieval
* analytics event emission

The service depends on interfaces rather than concrete infrastructure where practical.

This makes important behavior testable without requiring Redis or PostgreSQL for every unit test.

---

# 6. Repository Layer

Location:

```text
internal/repository/
```

PostgreSQL access is isolated behind repository interfaces.

The URL repository provides operations such as:

```text
Create
GetByID
GetByAlias
```

The analytics repository provides:

```text
RecordRedirect
GetAnalytics
```

Database-specific behavior such as PostgreSQL's `23505` unique-constraint error is translated into domain-level repository errors.

This prevents PostgreSQL-specific error handling from leaking throughout the service layer.

---

# 7. Base62

Location:

```text
internal/base62/
```

Generated numeric IDs are converted into compact short codes using Base62.

Alphabet:

```text
0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz
```

The implementation also validates invalid characters and protects against integer overflow during decoding.

The Base62 implementation is independently unit tested.

---

# 8. Redis Cache

Location:

```text
internal/cache/
```

The cache abstraction provides:

```text
Get
Set
Delete
```

The current implementation uses Redis.

Cache key format:

```text
url:{code}
```

Cached value:

```json
{
  "url_id": 1234567,
  "original_url": "https://example.com",
  "expires_at": null
}
```

TTL:

```text
24 hours
```

Redis is treated as a performance optimization.

A Redis failure does not automatically fail a redirect.

---

# 9. Analytics Worker

Location:

```text
internal/analytics/
```

The worker receives redirect events through a buffered channel.

Default configuration:

```text
BufferSize:  1000
FlushSize:    100
FlushPeriod:    1s
```

Events are flushed in batches.

The worker is deliberately non-blocking:

```text
redirect request
      │
      ├── redirect immediately
      │
      └── enqueue analytics event
```

If the channel is full:

```text
event dropped
redirect continues
```

The application increments:

```text
url_analytics_events_dropped_total
```

when this occurs.

Failed database batches are retained for a later retry.

The worker also attempts to flush accepted events during graceful shutdown using a bounded shutdown period.

---

# 10. Metrics

Location:

```text
internal/metrics/
```

Prometheus metrics include:

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

Dynamic URL values are not used as metric labels.

For example:

```text
GET /{code}
```

is used as the route label instead of:

```text
GET /abc123
GET /docs
GET /xyz789
```

This prevents unbounded metric-cardinality growth.

---

# 11. Database

The local development environment uses PostgreSQL through Docker Compose.

Core URL table:

```sql
CREATE TABLE urls (
    id BIGSERIAL PRIMARY KEY,
    original_url TEXT NOT NULL,
    custom_alias VARCHAR(64),
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

Custom aliases:

```sql
CREATE UNIQUE INDEX idx_urls_custom_alias
ON urls(custom_alias)
WHERE custom_alias IS NOT NULL;
```

Expiration lookup support:

```sql
CREATE INDEX idx_urls_expires_at
ON urls(expires_at)
WHERE expires_at IS NOT NULL;
```

Analytics:

```sql
CREATE TABLE url_analytics (
    url_id BIGINT PRIMARY KEY REFERENCES urls(id) ON DELETE CASCADE,
    redirect_count BIGINT NOT NULL DEFAULT 0,
    last_accessed_at TIMESTAMPTZ
);
```

---

# 12. Local Development

Start PostgreSQL and Redis:

```powershell
docker compose up -d
```

Run the server:

```powershell
go run ./cmd/server
```

The default development server listens on:

```text
http://localhost:8080
```

Health check:

```powershell
curl http://localhost:8080/health
```

Metrics:

```powershell
curl http://localhost:8080/metrics
```

---

# 13. Create a URL

PowerShell example:

```powershell
$body = @{
    url = "https://example.com/very/long/path"
} | ConvertTo-Json

Invoke-RestMethod `
    -Method Post `
    -Uri "http://localhost:8080/v1/urls" `
    -ContentType "application/json" `
    -Body $body
```

The response contains the generated code and short URL.

---

# 14. Redirect

Given a generated code:

```powershell
curl -i http://localhost:8080/B
```

A successful redirect returns:

```text
HTTP/1.1 302 Found
Location: https://example.com/...
```

---

# 15. Metadata

Retrieve metadata:

```powershell
curl http://localhost:8080/v1/urls/B
```

The response includes:

```text
code
original_url
created_at
expires_at
redirect_count
last_accessed_at
```

---

# 16. Testing

Run the complete Go test suite:

```powershell
go test ./...
```

The project contains tests for:

* Base62 encoding/decoding
* cache behavior
* repository behavior
* service validation
* redirect behavior
* expiration
* cache failure isolation
* analytics failure isolation
* analytics worker batching
* analytics worker retry behavior
* analytics worker buffer drops
* metrics
* HTTP handlers

On the development Windows environment, Application Control may block temporary Go test executables created under the user's temporary directory.

When that occurs, the affected package can be compiled explicitly:

```powershell
go test -c -o handler.test.exe ./internal/handler
.\handler.test.exe -test.v
Remove-Item .\handler.test.exe
```

This is an environment execution-policy issue rather than an application test failure.

---

# 17. Load Testing

The permanent redirect load-test script is:

```text
tests/load/redirect.js
```

The repository intentionally keeps load testing outside the production server code.

Temporary benchmark-only endpoints used during performance investigation were removed after measurement.

The observed hot-cache benchmark was approximately:

```text
50 VUs
30 seconds

~4,298 req/s
p50  10.03 ms
p95  20.81 ms
0% errors
```

A separate database-path benchmark against approximately one million rows observed approximately:

```text
~3,393 req/s
p50   5.86 ms
p95  15.01 ms
0% errors
```

These results are environment-specific measurements and should not be interpreted as universal production capacity.

---

# 18. Graceful Shutdown

The server handles shutdown signals and stops components in an orderly manner.

The shutdown sequence includes:

```text
Shutdown signal
      │
      ▼
Stop accepting new HTTP requests
      │
      ▼
Stop analytics worker
      │
      ▼
Flush accepted analytics events
      │
      ▼
Close Redis
      │
      ▼
Close PostgreSQL pool
      │
      ▼
Process exits
```

The HTTP shutdown and analytics worker shutdown are bounded by timeouts.

This prevents shutdown from waiting indefinitely on a failed dependency.

---

# 19. Configuration

Development defaults are provided for local execution.

Typical local values include:

```text
PostgreSQL:
postgres://shortener:shortener@localhost:5432/shortener

Redis:
redis://localhost:6379

Base URL:
http://localhost:8080
```

These defaults are intended for the local development environment.

Production deployments should provide database credentials, Redis configuration, and other environment-specific values through deployment configuration or secret management rather than relying on development defaults.

---

# 20. Production Considerations

The current implementation is deliberately smaller than a fully distributed production platform.

Before a high-scale production deployment, evaluate:

* PostgreSQL HA/failover
* PostgreSQL read replicas
* Redis replication or clustering
* durable analytics messaging
* distributed ID generation
* cache stampede protection
* rate limiting
* authentication/authorization
* malicious URL detection
* secret management
* TLS termination
* centralized logging
* alerting
* multi-region availability

These are intentionally outside the current V1 implementation.
