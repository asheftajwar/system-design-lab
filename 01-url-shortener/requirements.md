# URL Shortener — Requirements

## 1. Problem

Build a URL-shortening service that converts long URLs into short, shareable URLs.

Example:

```text
Input:
https://example.com/products/category/item?id=12345

Output:
https://sho.rt/aB91x
```

When a user visits the short URL, the service resolves the mapping and returns an HTTP redirect to the original URL.

The implementation is intentionally focused on the core shortening and redirect problem rather than authentication, billing, custom domains, or advanced analytics.

---

# 2. Functional Requirements

## 2.1 Create Short URL

The system must accept a URL and create a persistent short-code mapping.

Endpoint:

```http
POST /v1/urls
```

Example request:

```json
{
  "url": "https://example.com/products/item?id=12345"
}
```

Optional fields:

```json
{
  "url": "https://example.com/products/item?id=12345",
  "expires_at": "2027-01-01T00:00:00Z",
  "custom_alias": "docs"
}
```

Example response:

```json
{
  "code": "aB91x",
  "short_url": "https://sho.rt/aB91x",
  "expires_at": null
}
```

The mapping must be persisted before the creation request succeeds.

---

## 2.2 Redirect

A client can access:

```http
GET /aB91x
```

The service resolves the short code and returns:

```http
302 Found
Location: https://example.com/products/item?id=12345
```

Possible redirect outcomes include:

```text
302 Found       URL resolved successfully
404 Not Found   code does not exist
410 Gone        URL exists but has expired
500 Internal    unexpected server failure
```

---

## 2.3 Generated Short Codes

Generated codes must be unique.

The current implementation uses:

```text
PostgreSQL BIGSERIAL ID
        ↓
Base62 encoding
        ↓
short code
```

This provides deterministic uniqueness without random collision retries.

With seven Base62 characters:

```text
62^7 ≈ 3.5 trillion
```

possible combinations exist.

The implementation does not require a fixed seven-character code length.

---

## 2.4 URL Validation

The service must reject invalid URLs.

The current implementation requires:

* non-empty URL
* syntactically parseable URL
* HTTP or HTTPS scheme
* non-empty host
* maximum URL length of 2048 characters

Unsupported schemes such as:

```text
ftp://
javascript:
file://
```

are rejected.

---

## 2.5 Expiration

A URL may optionally have an expiration timestamp.

Example:

```json
{
  "url": "https://example.com",
  "expires_at": "2027-01-01T00:00:00Z"
}
```

The expiration timestamp must be in the future when creating the URL.

After expiration:

```http
GET /aB91x
```

returns:

```http
410 Gone
```

Expiration is checked from the URL's `expires_at` value rather than relying exclusively on Redis TTL.

---

## 2.6 Custom Alias

The API supports an optional custom alias.

Example:

```json
{
  "url": "https://example.com/docs",
  "custom_alias": "docs"
}
```

Result:

```text
https://sho.rt/docs
```

Alias requirements:

```text
Length:     1–64 characters
Characters: A-Z, a-z, 0-9, -, _
```

Aliases must be unique.

If an alias already exists, the API returns:

```http
409 Conflict
```

PostgreSQL enforces uniqueness through a partial unique index.

---

## 2.7 URL Metadata

The system provides basic metadata through:

```http
GET /v1/urls/{code}
```

Example:

```json
{
  "code": "aB91x",
  "original_url": "https://example.com/products/item",
  "created_at": "2026-09-25T00:00:00Z",
  "expires_at": null,
  "redirect_count": 1542,
  "last_accessed_at": "2026-09-25T02:30:00Z"
}
```

The metadata endpoint supports both generated codes and custom aliases.

---

## 2.8 Basic Analytics

The system records:

* redirect count
* last accessed time
* creation time

Redirect analytics are processed asynchronously.

The redirect request must not synchronously wait for an analytics database write.

The current V1 implementation intentionally allows analytics events to be dropped when the worker buffer is full.

Dropped events are observable through:

```text
url_analytics_events_dropped_total
```

---

# 3. Non-Functional Requirements

## 3.1 Availability

Target:

```text
99.9% redirect availability
```

The redirect path should continue operating when Redis is unavailable, provided PostgreSQL remains available.

Cached redirects may continue operating during a PostgreSQL outage, subject to cache availability and cache contents.

---

## 3.2 Latency

Initial target for redirect requests:

```text
p50 < 50 ms
p95 < 100 ms
p99 < 200 ms
```

These are service-side targets and exclude arbitrary network latency and the latency of the destination server.

Actual load-test results are documented separately and must not be interpreted as a universal production capacity guarantee.

---

## 3.3 Scalability

The API layer must be horizontally scalable.

The architecture should support multiple stateless API instances behind a load balancer.

The workload is expected to be strongly read-heavy:

```text
Redirects >> URL creations
```

---

## 3.4 Durability

Successfully created URL mappings must survive:

* API process restarts
* API instance failures
* Redis cache loss

PostgreSQL is therefore the persistent source of truth.

---

## 3.5 Consistency

Generated short-code uniqueness must be strongly guaranteed.

Custom alias uniqueness must be strongly guaranteed.

The database is responsible for the final uniqueness constraint.

Redis is derived state and may be lost or repopulated without losing URL mappings.

---

## 3.6 Graceful Degradation

Redis is not a hard dependency for redirect correctness.

If Redis cannot be read:

```text
Redis failure
    ↓
PostgreSQL lookup
    ↓
redirect
```

If Redis cannot be written after a PostgreSQL lookup, the redirect can still succeed.

Analytics is also not a hard dependency for redirects.

If an analytics event cannot be accepted by the worker:

```text
analytics failure/drop
        ↓
redirect continues
```

---

# 4. Scale Assumptions

These figures are design-exercise assumptions rather than production measurements.

Assume:

```text
Total stored URLs:             100 million
New URLs created per day:      1 million
Redirects per day:             100 million
Peak traffic multiplier:       5x average
Average original URL size:     200 bytes
```

This produces a strongly read-heavy workload.

Approximate ratio:

```text
Redirects : Creates
100 : 1
```

---

# 5. Traffic Estimates

## URL Creation

```text
1,000,000 / 86,400
≈ 11.6 requests/sec average
```

Rounded:

```text
≈ 12 writes/sec average
```

With a 5x peak assumption:

```text
≈ 60 writes/sec peak
```

---

## Redirect Traffic

```text
100,000,000 / 86,400
≈ 1,157 requests/sec average
```

Rounded:

```text
≈ 1.2K redirects/sec average
```

With a 5x peak assumption:

```text
≈ 5.8K redirects/sec peak
```

Therefore the design exercise targets approximately:

```text
Creates:
~60 writes/sec peak

Redirects:
~6K reads/sec peak
```

These are planning assumptions, not measured limits of the implementation.

---

# 6. Storage Estimate

A rough planning model:

```text
Original URL:       ~200 bytes
Short-code data:     small/inferred from ID
Metadata/indexes:   additional overhead
```

A simplified estimate of approximately:

```text
~300 bytes per mapping
```

gives:

```text
100,000,000 × 300 bytes
≈ 30 GB
```

Actual PostgreSQL storage will be higher because of:

* row overhead
* indexes
* page overhead
* WAL
* replication
* timestamps
* analytics data

A practical planning estimate is:

```text
~50–100 GB
```

for the primary URL mapping dataset and associated indexes, before detailed production capacity planning.

---

# 7. API Requirements

## Create URL

```http
POST /v1/urls
```

Request:

```json
{
  "url": "https://example.com/very/long/path",
  "expires_at": null,
  "custom_alias": null
}
```

Success:

```http
201 Created
```

Response:

```json
{
  "code": "aB91x",
  "short_url": "https://sho.rt/aB91x",
  "expires_at": null
}
```

Relevant error classes include:

```text
400 Bad Request
409 Conflict
500 Internal Server Error
```

---

## Redirect

```http
GET /{code}
```

Success:

```http
302 Found
```

Not found:

```http
404 Not Found
```

Expired:

```http
410 Gone
```

---

## URL Metadata

```http
GET /v1/urls/{code}
```

Success:

```http
200 OK
```

The response includes:

* code
* original URL
* creation time
* expiration time
* redirect count
* last access time

---

## Health

```http
GET /health
```

The service exposes a health endpoint for basic process/service health checks.

---

## Metrics

```http
GET /metrics
```

Prometheus-compatible metrics are exposed for HTTP traffic, cache behavior, database lookups, redirects, creations, and dropped analytics events.

---

# 8. Out of Scope

The current implementation does not provide:

* user authentication
* user accounts
* teams or organizations
* billing
* custom domains
* QR-code generation
* geographic analytics
* device analytics
* referrer analytics
* malicious URL scanning
* browser extensions
* mobile applications
* advanced analytics dashboards
* multi-region deployment
* PostgreSQL read replicas
* Kafka or another durable event stream
* Kubernetes deployment
* distributed ID generation
* cache stampede protection
* distributed locking

These may be evaluated in future iterations if workload measurements or product requirements justify them.

---

# 9. Current Performance Evidence

The implementation has been exercised with k6.

These results are benchmark observations under the tested local environment, not production capacity guarantees.

## Hot-cache redirect test

```text
Virtual users: 50
Duration:      30 seconds

Requests:      128,996
Throughput:    ~4,298 req/s
Average:       11.17 ms
p50:           10.03 ms
p90:           17.45 ms
p95:           20.81 ms
Maximum:       205.52 ms
Errors:        0%
```

## Database-path test

Approximately one million URL rows were present during the test.

```text
Virtual users: 25
Duration:      30 seconds

Requests:      101,800
Throughput:    ~3,393 req/s
Average:       ~7 ms
p50:           5.86 ms
p90:           12.19 ms
p95:           15.01 ms
Maximum:       131.24 ms
Errors:        0%
```

These measurements demonstrate behavior under the tested workload; they do not establish a universal maximum throughput.

---

# 10. Success Criteria

The V1 implementation is considered complete when it can:

1. Create a short URL.
2. Persist the URL mapping in PostgreSQL.
3. Generate deterministic unique short codes.
4. Redirect using generated short codes.
5. Support custom aliases.
6. Enforce custom alias uniqueness.
7. Validate HTTP/HTTPS URLs.
8. Enforce URL length and alias validation.
9. Support URL expiration.
10. Return `410 Gone` for expired redirects.
11. Return metadata through `GET /v1/urls/{code}`.
12. Record redirect count and last-accessed time asynchronously.
13. Continue redirects when Redis is unavailable, provided PostgreSQL can resolve the mapping.
14. Keep analytics failures out of the redirect critical path.
15. Expose operational Prometheus metrics.
16. Gracefully shut down the HTTP server and analytics worker.
17. Pass the automated test suite, subject to the known Windows Application Control execution restriction in the local environment.
18. Provide reproducible load-test scripts.
19. Demonstrate measured redirect performance under representative local load.
20. Keep benchmark-only production endpoints out of the final implementation.
