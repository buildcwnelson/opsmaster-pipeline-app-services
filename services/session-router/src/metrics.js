import client from "prom-client";

// Collect standard Node.js runtime metrics (event loop lag, memory, CPU)
client.collectDefaultMetrics({
  prefix: "opsmaster_session_router_",
});

export const httpRequestsTotal = new client.Counter({
  name: "opsmaster_session_router_http_requests_total",
  help: "Total number of HTTP requests processed by endpoint and status.",
  labelNames: ["method", "path", "status"],
});

export const httpRequestDurationSeconds = new client.Histogram({
  name: "opsmaster_session_router_http_request_duration_seconds",
  help: "Histogram of HTTP request latencies.",
  labelNames: ["method", "path"],
  buckets: [0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5],
});

export const activeSessionsGauge = new client.Gauge({
  name: "opsmaster_session_router_active_sessions_count",
  help: "Number of active WebRTC telehealth sessions.",
});

export const activePeersGauge = new client.Gauge({
  name: "opsmaster_session_router_active_peers_count",
  help: "Number of connected WebRTC peers across all sessions.",
});

export const signalingMessagesTotal = new client.Counter({
  name: "opsmaster_session_router_signaling_messages_total",
  help: "Count of WebRTC signaling messages processed.",
  labelNames: ["type", "direction"],
});

export { client as prometheus };
