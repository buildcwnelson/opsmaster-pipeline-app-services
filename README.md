# OpsMaster Streamlined 3-Container HealthTech Platform

Production-ready, HIPAA-hardened HealthTech Telehealth & Medical Vitals platform refactored into a high-performance, consolidated **3-container runtime architecture** with on-demand canary load testing.

---

## 1. Consolidated 3-Container Architecture

```
                       +----------------------------------+
                       |    k6 Telemetry Simulator        |
                       | (1,000+ Virtual Sensor Wearables)|
                       |   [Profile: "test" (On-Demand)]  |
                       +----------------+-----------------+
                                        |
                                        | POST /api/v1/vitals
                                        v
+---------------------------------------------------------------------------------+
|                       CONTAINER 1: app-engine (Go)                              |
|                                                                                 |
|  +--------------------+   In-Memory Channel   +------------------------------+  |
|  |   Vitals Ingress   |======================>|   Alert Rules Engine Worker  |  |
|  |  (REST + WebSockets|                       |  (Sliding-Window Evaluation) |  |
|  +---------+----------+                       +--------------+---------------+  |
|            |                                                 |                  |
|            | XADD vitals:stream                              |                  |
|            v                                                 v                  |
|  +--------------------+                       +------------------------------+  |
|  | WebRTC Telehealth  |                       | Clinical DevOps Diagnostics  |  |
|  |  (Signaling & State|                       |  (/api/v1/admin/diagnose)    |  |
|  +---------+----------+                       +--------------+---------------+  |
+------------|-------------------------------------------------|------------------+
             |                                                 |
             | Redis Stream & Sessions                         | SQL (Alerts, Logs)
             v                                                 v
+-------------------------------+             +-----------------------------------+
|      CONTAINER 2: redis       |             |      CONTAINER 3: postgres        |
|          (Redis 7)            |             |          (PostgreSQL 16)          |
| • Vitals Stream Buffer        |             | • Patient Demographics            |
| • WebRTC Session State        |             | • Telemetry Anomaly Alerts        |
| • Emergency Alert Fanout      |             | • Diagnostic Post-Mortem Records  |
+-------------------------------+             +-----------------------------------+
```

---

## 2. Directory Structure

```
opsmaster-pipeline/
├── .env.example                     # Environment configuration template
├── .gitignore                       # Clean Git exclusion rules
├── README.md                        # Platform architecture & runbook
├── docker-compose.yml               # Consolidated 3-container topology + k6 profile
├── app-services/                    # Unified Go application codebase
│   ├── Dockerfile                   # Multi-stage minimal Alpine build (HIPAA non-root)
│   ├── go.mod                       # Go module dependencies
│   ├── go.sum                       # Cryptographic checksums
│   ├── cmd/
│   │   └── server/
│   │       └── main.go              # Microservice entrypoint & graceful shutdown
│   └── pkg/
│       ├── alerts/                  # In-memory sliding-window anomaly worker
│       │   └── alerts.go
│       ├── cache/                   # Redis Streams & WebRTC session state store
│       │   └── redis.go
│       ├── config/                  # 12-factor environment loader
│       │   └── config.go
│       ├── database/                # PostgreSQL persistence layer with connection pool
│       │   └── db.go
│       ├── diagnostics/             # Admin triage diagnostics (/api/v1/admin/diagnose)
│       │   └── diagnostics.go
│       ├── ingress/                 # Vitals payload validation (REST + WebSockets)
│       │   └── ingress.go
│       ├── logger/                  # slog structured JSON logging & correlation IDs
│       │   └── logger.go
│       ├── metrics/                 # Prometheus metrics registry & middleware
│       │   └── metrics.go
│       └── webrtc/                  # WebRTC signaling router & telehealth sessions
│           └── webrtc.go
├── scripts/
│   ├── init.sql                     # PostgreSQL schema with patients & indexes
│   └── k6/
│       └── telemetry-load.js        # Synthetic sensor streaming load test (1,000+ VUs)
└── tests/
    └── verify_3container.sh         # Automated end-to-end integration test suite
```

---

## 3. Operational Runbook: Commands

### A. Build and Start Platform (3 Core Containers)
```bash
# Copy environment configuration
cp .env.example .env

# Build and start the 3 core runtime containers (app-engine, postgres, redis)
docker compose up -d --build

# Verify all 3 containers are healthy and running
docker compose ps
```

### B. Run On-Demand Load Testing with k6
The `k6` container is bound to the `test` profile and only executes when explicitly invoked:
```bash
# Execute canary load test with 1,000+ virtual sensors
docker compose --profile test run --rm k6

# Override load test target or sensor count dynamically:
docker compose --profile test run --rm -e VIRTUAL_DEVICES=2000 -e INJECT_ANOMALIES=true k6
```

### C. Inspect Structured JSON Logs
All services emit JSON logs with `X-Request-ID` correlation tags:
```bash
# Follow app-engine structured logs in real time
docker compose logs -f app-engine

# Filter for critical medical anomalies:
docker compose logs app-engine | grep "CRITICAL MEDICAL ANOMALY"
```

### D. Verify Observability & Administrative Diagnostics
```bash
# Liveness Probe (process responsiveness)
curl -s http://localhost:8080/livez | jq

# Readiness Probe (PostgreSQL and Redis connectivity)
curl -s http://localhost:8080/healthz | jq

# Prometheus Metrics Scrape
curl -s http://localhost:8080/metrics | grep opsmaster_app_engine_

# Clinical DevOps Diagnostic Triage Report
curl -s http://localhost:8080/api/v1/admin/diagnose | jq
```

### E. Run Local Automated Test Suite
```bash
./tests/verify_3container.sh
```

---

## 4. API Endpoints Reference

| Category | Method | Path | Description |
| :--- | :--- | :--- | :--- |
| **Ingress** | `POST` | `/api/v1/vitals` | Ingest single or batch telemetry JSON records |
| **Ingress** | `GET` | `/api/v1/vitals/ws` | Continuous sensor telemetry streaming via WebSocket |
| **Telehealth** | `POST` | `/api/v1/telehealth/sessions` | Create doctor-patient consultation session |
| **Telehealth** | `GET` | `/api/v1/telehealth/sessions` | List active telehealth consultation sessions |
| **Telehealth** | `GET` | `/api/v1/telehealth/sessions/{id}` | Retrieve session state and connected peers |
| **Telehealth** | `POST` | `/api/v1/telehealth/sessions/{id}/terminate` | Gracefully end consultation and disconnect peers |
| **Telehealth** | `GET` | `/api/v1/telehealth/ws` | WebRTC SDP offer/answer & ICE candidate router |
| **DevOps** | `GET` | `/api/v1/admin/diagnose` | Automated cluster triage & system diagnostic report |
| **DevOps** | `GET/POST`| `/api/v1/admin/logs` | Query or record incident post-mortem audit records |
| **Probes** | `GET` | `/livez` | Liveness healthcheck probe |
| **Probes** | `GET` | `/healthz` | Readiness healthcheck probe (DB + Redis) |
| **Metrics** | `GET` | `/metrics` | Prometheus metrics scrape endpoint |
