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

When a user visits the short URL, the service redirects them to the original URL.

---

## 2. Functional Requirements

### 2.1 Create Short URL

The system must allow a client to submit a long URL and receive a unique short URL.

```http
POST /v1/urls
Content-Type: application/json

{
  "url": "https://example.com/products/item?id=12345"
}
```

Response:

```json
{
  "short_url": "https://sho.rt/aB91x",
  "code": "aB91x"
}
```

### 2.2 Redirect

A client can access the short URL:

```http
GET /aB91x
```

The service resolves the short code and redirects the client to the original URL.

Expected response:

```http
HTTP/1.1 302 Found
Location: https://example.com/products/item?id=12345
```

### 2.3 Unique Short Codes

Every active URL must have a unique short code.

Example:

```text
aB91x → URL A
7Kp2m → URL B
z91Qa → URL C
```

Two different URLs must not accidentally receive the same active code.

### 2.4 URL Validation

The service must reject malformed or unsupported URLs.

At minimum:

* URL must be syntactically valid.
* HTTP and HTTPS URLs are supported.
* Empty URLs are rejected.

### 2.5 Expiration

A URL may optionally have an expiration time.

Example:

```json
{
  "url": "https://example.com",
  "expires_at": "2027-01-01T00:00:00Z"
}
```

After expiration, the short URL must no longer redirect to the original URL.

### 2.6 Custom Alias

The API may optionally allow a user to request a custom short code.

Example:

```json
{
  "url": "https://example.com",
  "custom_alias": "docs"
}
```

Result:

```text
https://sho.rt/docs
```

The alias must be unique.

### 2.7 Basic Analytics

The system should record basic access information:

* number of redirects
* creation time
* last accessed time

Detailed analytics such as geographic location, device information, and referrer analysis are out of scope for the initial version.

---

# 3. Non-Functional Requirements

## 3.1 Availability

The redirect path should be highly available because users depend on the short URL to reach the destination.

Target:

```text
99.9% availability
```

for the redirect endpoint.

The URL-creation endpoint may tolerate slightly more latency and occasional temporary unavailability.

---

## 3.2 Latency

Target latency for redirect requests:

```text
p50 < 50 ms
p95 < 100 ms
p99 < 200 ms
```

These targets refer to server-side processing and do not include arbitrary Internet or destination-server latency.

---

## 3.3 Scalability

The service must support horizontal scaling of API servers.

The architecture should be capable of handling substantially higher read traffic than write traffic.

---

## 3.4 Durability

Once a URL has been successfully created, its mapping should survive individual application-server failures.

The persistent URL mapping must therefore be stored outside the API process.

---

## 3.5 Consistency

URL creation requires strong uniqueness guarantees.

For example:

```text
custom alias "docs"
```

must not simultaneously belong to two different URLs.

Redirect reads can tolerate limited eventual consistency in some future optimizations, provided newly created URLs become available within an acceptable period.

---

# 4. Scale Assumptions

These are exercise assumptions rather than real-world measurements.

Assume:

```text
Total stored URLs:             100 million
New URLs created per day:      1 million
Redirects per day:             100 million
Peak traffic multiplier:       5x average
Average URL size:              200 bytes
Short-code length:             7 characters
```

The system is therefore strongly read-heavy.

Approximate ratio:

```text
Redirects : Creates
100 : 1
```

---

# 5. Traffic Estimates

## URL Creation

1 million new URLs per day:

```text
1,000,000 / 86,400
≈ 11.6 requests/sec
```

Average:

```text
≈ 12 writes/sec
```

Assuming a 5x peak factor:

```text
≈ 60 writes/sec peak
```

---

## Redirect Traffic

100 million redirects per day:

```text
100,000,000 / 86,400
≈ 1,157 requests/sec
```

Average:

```text
≈ 1.2K requests/sec
```

With a 5x peak factor:

```text
≈ 5.8K requests/sec peak
```

Therefore, the initial capacity target is approximately:

```text
Create:
~60 writes/sec peak

Redirect:
~6K reads/sec peak
```

---

# 6. Storage Estimate

Assume approximately:

```text
original URL:        200 bytes
short code:           7 bytes
metadata/indexes:   ~100 bytes
```

Approximate storage per mapping:

```text
~300 bytes
```

For 100 million URLs:

```text
100,000,000 × 300 bytes
≈ 30 GB
```

This is a rough estimate.

Real database storage will be higher because of:

* row overhead
* indexes
* page overhead
* replication
* WAL
* timestamps
* analytics data

A practical initial planning estimate is therefore:

```text
~50–100 GB
```

for the primary URL-mapping dataset and indexes.

---

# 7. Core API

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

Response:

```json
{
  "code": "aB91x",
  "short_url": "https://sho.rt/aB91x",
  "expires_at": null
}
```

---

## Redirect

```http
GET /{code}
```

Possible responses:

```text
302 Found
```

or:

```text
404 Not Found
```

or:

```text
410 Gone
```

for an expired URL.

---

## Get URL Metadata

```http
GET /v1/urls/{code}
```

Example response:

```json
{
  "code": "aB91x",
  "url": "https://example.com/very/long/path",
  "created_at": "2026-09-25T00:00:00Z",
  "expires_at": null,
  "redirect_count": 1542,
  "last_accessed_at": "2026-09-25T02:30:00Z"
}
```

---

# 8. Out of Scope

The initial system will not implement:

* user authentication
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
* multi-region deployment
* advanced analytics dashboards

These may be considered in later iterations.

---

# 9. Success Criteria

The initial implementation is successful when it can:

1. Create a short URL.
2. Persist the mapping.
3. Redirect using the short code.
4. Guarantee short-code uniqueness.
5. Validate URLs.
6. Support expiration.
7. Support custom aliases.
8. Record basic redirect statistics.
9. Survive application-server restarts.
10. Be horizontally scalable at the API layer.
11. Pass automated unit and integration tests.
12. Demonstrate performance under a representative load test.
