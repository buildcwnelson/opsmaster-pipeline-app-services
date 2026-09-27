from prometheus_client import Counter, Gauge, Histogram

TELEMETRY_CONSUMED_TOTAL = Counter(
    "opsmaster_alert_engine_telemetry_consumed_total",
    "Total telemetry records consumed from stream.",
    ["metric_type", "status"]
)

ANOMALIES_DETECTED_TOTAL = Counter(
    "opsmaster_alert_engine_anomalies_detected_total",
    "Total medical anomalies detected by rule evaluator.",
    ["anomaly_type", "severity"]
)

ALERT_DISPATCH_TOTAL = Counter(
    "opsmaster_alert_engine_alert_dispatch_total",
    "Total alerts dispatched to sink.",
    ["sink_type", "status"]
)

RULE_EVAL_DURATION_SECONDS = Histogram(
    "opsmaster_alert_engine_rule_eval_duration_seconds",
    "Time taken to evaluate sliding window rules.",
    buckets=[0.0001, 0.0005, 0.001, 0.005, 0.01, 0.05, 0.1]
)

ACTIVE_PATIENTS_IN_WINDOW = Gauge(
    "opsmaster_alert_engine_active_patients",
    "Number of active patients tracked in the current sliding window."
)

WORKER_HEARTBEAT = Gauge(
    "opsmaster_alert_engine_worker_heartbeat_timestamp",
    "Unix timestamp of the most recent worker loop heartbeat."
)
