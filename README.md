# OpsMaster Real-Time Clinical Pipeline

Production-ready microservice pipeline for real-time patient vital sign ingestion, streaming anomaly detection, WebRTC telehealth session routing, synthetic canary load generation, and cluster DevOps diagnostics.

---

## Architecture Overview

```
                                +---------------------------+
                                |   k6 Sensor Simulator     |
                                | (1,000+ Virtual Wearables)|
                                +-------------+-------------+
                                              |
                                              | POST /api/v1/telemetry
                                              v
+-----------------------+           +---------------------------+
| OpenTelemetry Agents  |---------->|    vitals-ingress-api     |
| (OTLP /v1/metrics)    |           |   (Go High-Throughput)    |
+-----------------------+           +-------------+-------------+
                                                  |
                                                  | XADD vitals:stream
                                                  v
                                    +---------------------------+
                                    |    Redis 7.2 Cluster      |
                                    | (Streams + Pub/Sub + State)
                                    +-------------+-------------+
                                                  |
                                                  | XREADGROUP
                                                  v
+-----------------------+           +---------------------------+
| PostgreSQL 16 DB      |<----------|    alert-rules-engine     |
| (Alerts & Incidents)  |           | (Python Sliding Worker)   |
+-----------^-----------+           +-------------+-------------+
            |                                     |
            |                                     | Webhook HTTP POST
            |                                     v
+-----------+-----------+           +---------------------------+
|  WebRTC Telehealth    |<--------->| clinical-devops-assistant |
|    session-router     |           |     (FastAPI Cluster API) |
|   (Node.js / WS)      |           +---------------------------+
+-----------------------+
```

---

## Services & Functionality

### 1. `vitals-ingress-api` (Go)
- **Directory**: `services/vitals-ingress-api`
- **Port**: `8080`
- **Purpose**: Ultra-low latency, high-throughput ingestion API for medical sensor metrics (heart rate, SpO2, ECG waveforms, blood pressure, etc.).
- **Key Features**:
  - Validates telemetry payloads (`patient_id`, `device_id`, `metric_type`, `value` / `waveform`, `timestamp`).
  - Supports single payload and high-throughput batch ingestion (`POST /api/v1/telemetry`).
  - OTLP-compliant OpenTelemetry metrics ingestion endpoint (`POST /v1/metrics`).
  - High-performance Redis Stream publisher (`XADD` with capped stream length) and Pub/Sub channel broadcaster.
  - Zero-allocation structured JSON logging via Go standard library `log/slog`.
  - Prometheus metrics instrumentation (`/metrics`) via `promhttp`.
  - Health check probes (`/healthz` readiness, `/livez` liveness).
  - Graceful shutdown intercepting `SIGTERM`/`SIGINT`, draining active requests with configurable timeout.

### 2. `alert-rules-engine` (Python Streaming Worker)
- **Directory**: `services/alert-rules-engine`
- **Port**: `8081`
- **Purpose**: Real-time streaming worker consuming vital streams from Redis to detect critical medical anomalies.
- **Rules Evaluated**:
  - **Tachycardia**: Sustained heart rate > 140 BPM across sliding time window.
  - **Bradycardia**: Sustained heart rate < 45 BPM.
  - **Hypoxemia**: SpO2 < 90% (Warning) and SpO2 < 85% (Critical Arterial Desaturation).
  - **Hypertensive Crisis**: Systolic blood pressure >= 180 mmHg.
  - **ECG Arrhythmia**: Peak-to-peak amplitude spikes (> 4.0 mV) indicative of ventricular fibrillation.
- **Key Features**:
  - Sliding-window trend evaluation to eliminate single-packet jitter and false positives.
  - Multi-sink dispatch: PostgreSQL audit database, external Webhook HTTP POST (with retries), and Redis Pub/Sub broadcast (`alerts:critical`).
  - Dedicated observability HTTP server exposing `/metrics`, `/healthz`, `/livez`.
  - Graceful shutdown handling: drains in-flight stream messages, commits offsets (`XACK`), and cleanly closes database connection pools.

### 3. `session-router` (Node.js)
- **Directory**: `services/session-router`
- **Port**: `8082`
- **Purpose**: WebRTC signaling metadata router and session state tracker for live doctor-patient telehealth video consultations.
- **Key Features**:
  - WebSocket signaling gateway on `/ws/signal` routing `offer`, `answer`, and `ice-candidate` packets between peers.
  - Redis session state store tracking consultation lifecycle (`WAITING`, `CONNECTED`, `TERMINATED`).
  - REST control endpoints:
    - `POST /api/v1/sessions`: Create new consultation session.
    - `GET /api/v1/sessions/:id`: Retrieve session details and active peers.
    - `POST /api/v1/sessions/:id/terminate`: Gracefully end consultation.
  - Structured JSON logging using `pino` with correlation IDs.
  - Prometheus metrics via `prom-client` tracking active sessions, peers, and signaling message rates.
  - Clean shutdown: broadcasts close frame `1001` (Going Away) to active WebRTC peers, drains HTTP connections, and closes Redis connection.

### 4. `k6-sensor-simulator` (JavaScript for k6)
- **Directory**: `services/k6-sensor-simulator`
- **Purpose**: Synthetic sensor telemetry load generator for canary deployments and stress testing.
- **Key Features**:
  - Simulates 1,000+ virtual wearable devices emitting continuous heart rate, SpO2, and ECG waveforms.
  - Realistic physiological distributions with configurable anomaly injection (`INJECT_ANOMALIES=true`) and error injection (`INJECT_MALFORMED_RATE=0.01`).
  - Strict canary deployment SLA thresholds:
    - `http_req_failed < 0.01` (error rate below 1%)
    - `http_req_duration p(95) < 200ms` (95th percentile latency below 200ms)
    - `opsmaster_k6_ingest_success_rate > 0.99`
  - Automated summary reporter indicating canary promotion or rollback recommendation.

### 5. `clinical-devops-assistant` (FastAPI / Python)
- **Directory**: `services/clinical-devops-assistant`
- **Port**: `8083`
- **Purpose**: Operational diagnostics and incident post-mortem REST API for cluster SREs and healthcare DevOps engineers.
- **Key Features**:
  - `GET /api/v1/diagnose`: Real-time cluster health evaluation querying upstream services, analyzing telemetry drop rates and stream lag spikes.
  - `POST /api/v1/post-mortems`: Persistent incident post-mortem tracking backed by PostgreSQL.
  - `GET /api/v1/post-mortems`: Retrieve incident history with severity filters (`P0` to `P3`).
  - `GET /api/v1/cluster/overview`: Live cluster status aggregation.
  - Prometheus metrics via `prometheus-fastapi-instrumentator`.
  - Structured JSON request logging middleware.
  - Lifespan context manager for startup and graceful connection pool teardown.

---

## Observability & Production Standards

All 5 services conform to strict production standards:

### 1. Structured JSON Logging
Every log line is emitted to `stdout` in valid JSON format:
```json
{
  "timestamp": "2026-09-28T00:01:42.403Z",
  "level": "INFO",
  "service": "vitals-ingress-api",
  "request_id": "req-20260928-100234",
  "method": "POST",
  "path": "/api/v1/telemetry",
  "status": 202,
  "duration_ms": 2.41,
  "message": "HTTP request handled"
}
```

### 2. Standardized Health Probes
- `/livez` (Liveness Probe): Returns HTTP 200 if the process event loop is alive. Used by Kubernetes/Docker to detect deadlocks.
- `/healthz` (Readiness Probe): Returns HTTP 200 if dependencies (Redis, PostgreSQL) are connected and healthy, or HTTP 503 Service Unavailable if degraded.

### 3. Prometheus Metrics (`/metrics`)
Key metrics exposed across the pipeline:
| Service | Metric Name | Type | Description |
|---------|-------------|------|-------------|
| `vitals-ingress-api` | `opsmaster_vitals_ingress_http_requests_total` | Counter | Total HTTP ingestion requests by method, path, status |
| `vitals-ingress-api` | `opsmaster_vitals_ingress_http_request_duration_seconds` | Histogram | Request latency distribution |
| `vitals-ingress-api` | `opsmaster_vitals_ingress_vitals_ingested_total` | Counter | Ingested sensor points by metric type |
| `vitals-ingress-api` | `opsmaster_vitals_ingress_queue_latency_seconds` | Histogram | Redis stream publish duration |
| `alert-rules-engine` | `opsmaster_alert_engine_telemetry_consumed_total` | Counter | Stream events consumed |
| `alert-rules-engine` | `opsmaster_alert_engine_anomalies_detected_total` | Counter | Anomalies flagged by rule evaluator |
| `alert-rules-engine` | `opsmaster_alert_engine_active_patients` | Gauge | Patients in active sliding window |
| `session-router` | `opsmaster_session_router_active_sessions_count` | Gauge | Active telehealth consultations |
| `session-router` | `opsmaster_session_router_active_peers_count` | Gauge | Connected WebRTC WebSocket clients |
| `session-router` | `opsmaster_session_router_signaling_messages_total` | Counter | Signaling messages routed by type |
| `clinical-devops-assistant` | `opsmaster_devops_diagnoses_requested_total` | Counter | Diagnostic queries executed |
| `clinical-devops-assistant` | `opsmaster_devops_post_mortems_created_total` | Counter | Post-mortems recorded by severity |

### 4. Graceful Shutdown
Every service implements signal trapping (`SIGTERM`, `SIGINT`):
1. Stop accepting new incoming requests / WebSocket connections.
2. Allow in-flight requests and background stream tasks to drain within a configurable timeout (default 10s).
3. Safely disconnect database pools and Redis connections.
4. Exit cleanly with return code 0.

---

## Quickstart & Verification

### Running with Docker Compose
```bash
# Build and run all services with Redis and PostgreSQL
docker-compose up -d --build

# Run k6 sensor load test profile against vitals-ingress-api
docker-compose run --rm k6-sensor-simulator
```

### Running Automated Test Suite
```bash
# Run local end-to-end verification
./tests/verify_services.sh
```
