from prometheus_client import Counter, Histogram, Gauge

DIAGNOSES_REQUESTED_TOTAL = Counter(
    "opsmaster_devops_diagnoses_requested_total",
    "Total number of cluster diagnosis triage runs requested.",
    ["status"]
)

POST_MORTEMS_CREATED_TOTAL = Counter(
    "opsmaster_devops_post_mortems_created_total",
    "Total post-mortem incident reports recorded in PostgreSQL.",
    ["severity"]
)

DIAGNOSIS_LATENCY_SECONDS = Histogram(
    "opsmaster_devops_diagnosis_latency_seconds",
    "Time taken to gather telemetry errors and perform cluster diagnosis.",
    buckets=[0.01, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5]
)

DATABASE_HEALTH_STATUS = Gauge(
    "opsmaster_devops_database_health",
    "Current health status of the PostgreSQL database connection (1 = healthy, 0 = degraded)."
)
