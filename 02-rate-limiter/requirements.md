# Rate Limiting Platform — Requirements

## 1. Purpose

The Rate Limiting Platform provides centralized, distributed request enforcement for APIs and services.

It must support:

* high-throughput request decisions
* multiple rate-limiting algorithms
* authenticated and anonymous identities
* API keys
* tenant and plan-based policies
* endpoint-specific limits
* burst control
* API gateway integration
* persistent usage history
* asynchronous usage events
* observability
* horizontal scaling
* regional deployment
* controlled degradation during dependency failures

The system is designed to be reusable by multiple APIs and services rather than being tightly coupled to a single application.

---

# 2. Core Concepts

The platform separates five concepts:

```text
Identity
    ↓
Policy
    ↓
Rate-limit algorithm
    ↓
Decision
    ↓
Usage event
```

### Identity

The entity being limited.

Examples:

* user
* API key
* application
* tenant
* IP address
* service identity

### Policy

Defines what the identity is allowed to consume.

Example:

```text
PRO
POST /orders
100 requests/minute
burst capacity: 20
```

### Algorithm

Determines how requests are counted or admitted.

Supported algorithms:

* token bucket
* fixed window
* sliding-window log

The design also evaluates sliding-window counters as an alternative implementation.

### Decision

The synchronous result:

```text
ALLOW
or
REJECT
```

### Usage event

An asynchronous record of the decision used for:

* analytics
* historical reporting
* billing usage
* capacity analysis
* auditing

---

# 3. Functional Requirements

## 3.1 Request Evaluation

The platform MUST accept a rate-limit evaluation request containing enough information to determine:

* identity
* tenant
* HTTP method
* route/resource
* requested operation
* applicable policy
* request cost
* region

Conceptually:

```json
{
  "identity": {
    "type": "user",
    "id": "12345"
  },
  "tenant_id": "tenant-123",
  "method": "POST",
  "resource": "/orders",
  "cost": 1,
  "region": "us-east"
}
```

The platform MUST return:

* allowed/rejected
* applicable limit
* remaining capacity
* reset information
* retry information when rejected
* policy identifier

---

# 4. Rate-Limiting Algorithms

The platform MUST support multiple algorithms because different workloads require different enforcement characteristics.

## 4.1 Token Bucket

Token bucket is the primary general-purpose algorithm.

Configuration:

```text
capacity
refill rate
request cost
```

Example:

```text
capacity = 100
refill_rate = 10 tokens/sec
cost = 1
```

The bucket may temporarily absorb bursts up to its capacity while maintaining a long-term average rate.

Token bucket MUST support variable request costs.

Example:

```text
GET /products       cost = 1
POST /orders        cost = 2
POST /export        cost = 10
```

---

## 4.2 Fixed Window

The platform MUST support fixed-window limits.

Example:

```text
100 requests / 60 seconds
```

The counter resets at the beginning of each window.

Fixed window is intended for policies where simplicity and low state cost are more important than precise boundary behavior.

---

## 4.3 Sliding-Window Log

The platform MUST support a precise sliding-window implementation.

Example:

```text
100 requests during any rolling 60-second period
```

Request timestamps are retained for the active window.

The implementation MUST remove expired timestamps.

The system MUST protect against unbounded state growth.

---

## 4.4 Sliding-Window Counter

The architecture SHOULD support a sliding-window counter as an alternative to the full timestamp log.

This approach can provide lower memory usage than a sliding-window log while reducing fixed-window boundary effects.

Its suitability MUST be evaluated through measurement before becoming the default algorithm.

---

# 5. Multiple Simultaneous Limits

A single request MAY be subject to multiple policies.

Example:

```text
20 requests / second
500 requests / minute
50,000 requests / day
```

A request is allowed only when all applicable limits allow it.

The response MUST expose the relevant limit information without requiring clients to understand internal implementation details.

---

# 6. Identity Model

The platform MUST support multiple identity types.

### Supported identities

```text
User
API Key
Application
Tenant
IP Address
Service Identity
Anonymous
```

Identity resolution MUST follow an explicit precedence policy.

For example:

```text
Authenticated user
    ↓
API key/application
    ↓
Trusted gateway identity
    ↓
IP address
    ↓
Anonymous
```

The exact precedence MUST be configurable.

The platform MUST NOT trust client-supplied identity headers unless they originate from a trusted gateway or authenticated integration.

---

# 7. Authentication Integration

The platform MUST integrate with authenticated requests without becoming a full identity provider.

Supported integration mechanisms:

* API keys
* JWT claims
* trusted gateway identity
* service-to-service identity

For JWT-based integrations, the rate limiter consumes validated identity claims rather than independently becoming the system of record for user authentication.

Example identity:

```text
user_id = 12345
tenant_id = tenant-123
plan = pro
```

Authentication validation MAY be performed by the API gateway or an upstream authentication service.

---

# 8. API Gateway Integration

The platform MUST support deployment behind an API gateway.

Expected request path:

```text
Client
  ↓
API Gateway
  ↓
Authentication
  ↓
Rate Limiter
  ↓
Application Service
```

The rate limiter MUST expose an integration contract suitable for:

* gateway plugins
* HTTP middleware
* internal RPC clients
* service libraries

The core rate-limit engine MUST remain independent of a specific gateway product.

---

# 9. Policy Model

Policies define rate-limit behavior.

A policy MUST be able to specify:

* algorithm
* limit
* time interval
* burst capacity
* request cost
* identity scope
* resource scope
* tenant scope
* region
* enforcement mode
* policy priority

Example:

```json
{
  "id": "pro-orders-v1",
  "algorithm": "token_bucket",
  "rate": 100,
  "interval": "1m",
  "burst": 20,
  "resource": "POST:/orders",
  "scope": "tenant"
}
```

---

# 10. Billing Tiers

The platform MUST support plan-based rate-limit policies.

Example:

```text
Free
Pro
Business
Enterprise
```

Plans MAY define:

* request rate
* burst capacity
* daily quota
* endpoint-specific limits
* request costs
* regional limits

The rate limiter MUST NOT become the billing system itself.

Instead:

```text
Billing / Subscription System
          ↓
       Plan state
          ↓
    Policy configuration
          ↓
     Rate Limiter
```

Plan changes MUST eventually propagate to the rate-limiting policy layer.

The design MUST define behavior for policy propagation delays.

---

# 11. Tenant Isolation

The platform MUST support multi-tenant environments.

A tenant MAY have:

* its own limits
* its own policies
* its own API keys
* its own usage history
* its own billing plan

Tenant-specific state MUST NOT be accidentally shared with another tenant.

Rate-limit keys MUST include sufficient scope to prevent cross-tenant collisions.

---

# 12. Resource-Specific Policies

Policies MUST support resource-level enforcement.

Examples:

```text
GET /products
1000/minute

POST /orders
100/minute

POST /payments
20/minute

POST /exports
5/minute
```

The resource identity SHOULD use a normalized route rather than a raw URL containing arbitrary path parameters.

For example:

```text
POST /users/123/orders
```

should resolve to:

```text
POST:/users/{user_id}/orders
```

rather than creating a separate policy key for every user ID.

---

# 13. Redis State

Redis is the primary hot-state store for distributed rate-limit decisions.

The implementation MUST use atomic operations so concurrent API instances cannot incorrectly admit requests because of race conditions.

Conceptual state:

```text
rate_limit:{scope}:{policy}:{identity}:{resource}
```

The exact key format MUST provide:

* tenant isolation
* policy isolation
* identity isolation
* resource isolation
* regional isolation where required

Redis state MUST have bounded lifetime where the algorithm permits expiration.

---

# 14. Atomicity

Rate-limit decisions MUST be atomic from the perspective of concurrent requests.

For token bucket, the operation:

```text
read state
calculate refill
check capacity
consume tokens
write state
```

MUST be performed atomically.

The implementation SHOULD use a Redis Lua script or an equivalent atomic server-side mechanism.

A sequence of independent:

```text
GET
→ calculate
→ SET
```

operations is not sufficient for the distributed critical path.

---

# 15. Rate-Limit Responses

Allowed requests SHOULD expose standard rate-limit information.

Example:

```http
RateLimit-Limit: 100
RateLimit-Remaining: 73
RateLimit-Reset: 42
```

Rejected requests MUST return:

```http
HTTP/1.1 429 Too Many Requests
Retry-After: 17
```

The response SHOULD identify the applicable policy where exposing that information is safe.

---

# 16. Request Cost

The platform MUST support request costs greater than one.

Example:

```text
GET /products       cost 1
POST /orders        cost 2
POST /reports       cost 5
POST /exports       cost 20
```

A request MUST be rejected if its cost exceeds the currently available allowance.

This is particularly important for token-bucket policies.

---

# 17. Persistent Usage History

Rate-limit state in Redis is operational state, not the permanent historical record.

The system MUST support persistent historical usage.

History SHOULD store aggregated usage rather than one PostgreSQL row per request.

Example:

```text
tenant
identity
policy
resource
time_bucket
allowed_count
rejected_count
total_cost
```

Example:

```text
tenant-123
pro-orders-v1
POST:/orders
2026-10-02T12:00:00Z
8421 allowed
391 rejected
```

Historical retention MUST be configurable.

---

# 18. Kafka Event Streaming

Kafka SHOULD be used for asynchronous rate-limit usage events when persistent history, billing usage, analytics, or multiple downstream consumers are enabled.

The synchronous decision MUST NOT depend on Kafka availability.

The desired flow is:

```text
Request
   ↓
Rate-limit decision
   ↓
Redis
   ↓
Response

Decision event
   ↓
Kafka
   ├── Usage history
   ├── Billing
   ├── Analytics
   └── Monitoring
```

Kafka events SHOULD contain:

* event ID
* timestamp
* tenant
* identity
* policy
* resource
* decision
* request cost
* region
* algorithm version

Consumers MUST be idempotent.

---

# 19. Event Delivery

The system MUST define that usage events are **eventually consistent**.

A successful rate-limit decision MUST NOT be rolled back because an asynchronous usage event cannot be published.

The implementation MUST expose metrics for:

* event publication failures
* dropped events, if applicable
* consumer lag
* processing failures

If stronger delivery guarantees are required, the architecture SHOULD support an outbox or equivalent durable handoff mechanism.

---

# 20. Multi-Region Requirements

The architecture MUST account for multi-region deployment.

Regional rate limiting SHOULD support:

```text
Region A → local Redis
Region B → local Redis
Region C → local Redis
```

The design MUST explicitly distinguish:

### Regional limits

Example:

```text
100 requests/minute per region
```

from:

### Global limits

Example:

```text
100 requests/minute globally
```

A naive independent regional counter MUST NOT be presented as exact global enforcement.

---

# 21. Global Rate Limits

For globally enforced limits, the platform MUST support a documented strategy.

Potential strategies include:

### Centralized global state

Accurate but adds cross-region latency/dependency.

### Regional quota allocation

A global quota is divided among regions.

Example:

```text
Global: 1000/minute

US: 600
EU: 250
APAC: 150
```

### Token allocation

A global coordinator distributes token budgets to regional limiters.

### Bounded approximation

Allow a documented amount of enforcement error in exchange for lower latency.

The initial implementation MAY use regional enforcement while the global coordination design is developed and tested separately.

---

# 22. Failure Handling

## Redis unavailable

The system MUST have an explicit policy.

Possible modes:

### Fail-open

Requests continue when rate-limit state cannot be evaluated.

Useful when availability is more important than strict enforcement.

### Fail-closed

Requests are rejected when enforcement cannot be verified.

Useful for security-sensitive or expensive operations.

The enforcement mode MUST be configurable per policy/resource where appropriate.

The system MUST expose the number of requests affected by Redis failures.

---

## Kafka unavailable

Rate-limit decisions MUST continue.

Usage-event delivery MAY be delayed.

The system MUST provide a durable or bounded retry strategy according to the configured delivery guarantee.

---

## Policy store unavailable

The system SHOULD continue using cached policy configuration when safe.

Policy freshness and maximum stale duration MUST be defined.

---

## Configuration propagation failure

An API instance using an older policy MUST be observable.

Policy versions SHOULD be included in decisions and metrics.

---

# 23. Security Requirements

The platform MUST protect against:

* tenant isolation failures
* identity spoofing
* forged gateway headers
* API-key leakage
* unauthorized policy changes
* Redis key manipulation
* untrusted route/resource identifiers
* excessive cardinality attacks
* malicious identity creation

Sensitive credentials MUST NOT be stored in source code.

Administrative policy APIs MUST require authentication and authorization.

---

# 24. Abuse and Cardinality Protection

A malicious client must not be able to create unlimited rate-limit keys.

Examples of dangerous keys:

```text
rate_limit:{random-user-controlled-value}
```

The system MUST constrain identity and resource cardinality.

The platform SHOULD:

* validate identity formats
* normalize routes
* restrict custom policy dimensions
* monitor unique-key growth
* enforce maximum key lengths

---

# 25. Observability

The platform MUST expose metrics for:

### Decisions

```text
rate_limit_allowed_total
rate_limit_rejected_total
```

### Latency

```text
rate_limit_decision_duration_seconds
```

### Redis

```text
rate_limit_redis_errors_total
rate_limit_redis_latency_seconds
```

### Policies

```text
rate_limit_policy_evaluations_total
```

### Algorithms

Algorithm-specific counters SHOULD be available.

### Events

```text
rate_limit_events_published_total
rate_limit_event_publish_errors_total
```

### Kafka

The operational system SHOULD monitor:

* consumer lag
* processing errors
* retry counts
* dead-letter events

Metrics MUST support dimensions such as algorithm, policy, resource, region, and decision without creating uncontrolled metric cardinality.

---

# 26. Availability

The rate-limit decision path SHOULD be horizontally scalable.

Adding API instances MUST NOT create independent rate-limit state.

```text
API 1 ─┐
API 2 ─┼──> Shared rate-limit state
API 3 ─┘
```

Redis availability must therefore be treated as a critical dependency of strict enforcement.

The production architecture SHOULD support Redis high availability.

---

# 27. Performance Targets

The synchronous rate-limit decision should add minimal latency to the API path.

Initial production targets:

| Metric                 |                              Target |
| ---------------------- | ----------------------------------: |
| p50 decision latency   |                              < 2 ms |
| p95 decision latency   |                              < 5 ms |
| p99 decision latency   |                             < 10 ms |
| Decision errors        |                              < 0.1% |
| Horizontal scalability | Linear within infrastructure limits |

These are **engineering targets**, not measured results.

They must be validated using load tests.

---

# 28. Scale Targets

The initial platform should be designed around:

```text
10,000+ rate-limit decisions/sec
100,000+ configured policies
1M+ active identities
multiple API instances
multiple tenants
multiple regions
```

The architecture SHOULD have a path toward significantly higher throughput through:

* Redis clustering
* partitioning
* regional isolation
* efficient Lua scripts
* local policy caching
* event-stream partitioning

---

# 29. Data Retention

Persistent usage history MUST have configurable retention.

Example:

```text
Raw/near-term usage: 30 days
Aggregated usage: 12 months
Billing records: according to billing requirements
```

Retention policy must be independent from Redis state expiration.

---

# 30. Consistency Requirements

The system has different consistency requirements for different data.

| Data                      | Consistency                                  |
| ------------------------- | -------------------------------------------- |
| Rate-limit decision       | Strong/atomic within enforcement scope       |
| Redis bucket state        | Strong atomic mutation                       |
| Policy configuration      | Eventually consistent with bounded staleness |
| Usage events              | Eventually consistent                        |
| Historical analytics      | Eventually consistent                        |
| Billing aggregation       | Eventually consistent with reconciliation    |
| Global multi-region quota | Depends on selected strategy                 |

The architecture MUST NOT describe eventually consistent history as if it were authoritative real-time enforcement state.

---

# 31. Availability Versus Enforcement Tradeoff

The platform MUST make the following tradeoff explicit:

```text
Strict enforcement
        ↕
High availability
```

For some APIs:

```text
Redis unavailable
→ reject
```

may be correct.

For others:

```text
Redis unavailable
→ allow
```

may be preferable.

The system MUST therefore support policy-specific enforcement modes rather than assuming one universal failure behavior.

---

# 32. Administrative Configuration

The production system SHOULD provide an administrative interface for:

* creating policies
* updating policies
* assigning plans
* assigning policies to tenants
* configuring endpoint limits
* changing enforcement modes
* viewing policy versions
* managing regional allocations

Configuration changes MUST be versioned.

---

# 33. Auditability

Administrative changes MUST be auditable.

Audit records SHOULD contain:

```text
actor
timestamp
tenant
resource
previous configuration
new configuration
policy version
reason/change identifier
```

Audit history MUST be independent of high-volume request history.

---

# 34. Out of Scope

The platform itself will not become:

* a complete identity provider
* a complete billing platform
* a full API gateway
* a general-purpose analytics warehouse
* an application database
* a global service-discovery platform

It provides integration boundaries for these systems.

---

# 35. Success Criteria

The implementation is considered production-ready for its defined scope when:

1. Concurrent API instances enforce shared limits correctly.
2. Token bucket decisions are atomic.
3. Sliding-window log behavior is correct across window boundaries.
4. Multiple simultaneous limits are enforced correctly.
5. API-key and authenticated-user identities work correctly.
6. Tenant isolation is verified.
7. Gateway integration works without coupling the core engine to one gateway.
8. 429 responses and rate-limit headers are correct.
9. Redis failures behave according to configured enforcement mode.
10. Policy changes propagate with observable versioning.
11. Usage events do not block the synchronous decision path.
12. Persistent usage history survives Redis state expiration.
13. Kafka consumers are idempotent.
14. Metrics expose decision latency, rejection rates, dependency failures, and event health.
15. Concurrency/load testing demonstrates the target performance.
16. Multi-region behavior and global-limit limitations are explicitly tested and documented.
17. Security tests cover identity spoofing and tenant isolation.
18. The documented architecture matches the actual implementation and measured behavior.

---

# 36. Implementation Strategy

The implementation should proceed in controlled slices:

```text
1. Domain + policy model
2. Token bucket engine
3. Redis atomic state
4. HTTP/API contract
5. Identity/authentication integration
6. Fixed window
7. Sliding-window log
8. Multiple policies and request costs
9. Gateway adapter
10. Billing/plan policy integration
11. Prometheus observability
12. Kafka usage events
13. Persistent usage history
14. Failure testing
15. Concurrency testing
16. Load testing
17. Multi-region design/testing
18. Production documentation
```

Each phase should be tested independently before integration.
