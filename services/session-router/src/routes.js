import express from "express";
import { prometheus, httpRequestsTotal, httpRequestDurationSeconds } from "./metrics.js";
import { logger } from "./logger.js";

export function createRouter(sessionStore, redisClientWrapper) {
  const router = express.Router();
  const startTime = Date.now();

  // Metrics & Request logging middleware
  router.use((req, res, next) => {
    const start = process.hrtime();
    const requestId = req.headers["x-request-id"] || `req-${Date.now()}-${Math.random().toString(36).substring(2, 8)}`;
    res.setHeader("X-Request-ID", requestId);

    res.on("finish", () => {
      const diff = process.hrtime(start);
      const durationSeconds = diff[0] + diff[1] / 1e9;
      const durationMs = durationSeconds * 1000;

      httpRequestsTotal.labels(req.method, req.route ? req.route.path : req.path, res.statusCode.toString()).inc();
      httpRequestDurationSeconds.labels(req.method, req.route ? req.route.path : req.path).observe(durationSeconds);

      logger.info({
        requestId,
        method: req.method,
        path: req.originalUrl,
        status: res.statusCode,
        durationMs: Number(durationMs.toFixed(2)),
      }, "HTTP request completed");
    });

    next();
  });

  // Observability: Metrics
  router.get("/metrics", async (req, res) => {
    res.setHeader("Content-Type", prometheus.register.contentType);
    res.send(await prometheus.register.metrics());
  });

  // Observability: Liveness Probe
  router.get("/livez", (req, res) => {
    res.status(200).json({
      status: "alive",
      service: "session-router",
      uptimeSeconds: Number(((Date.now() - startTime) / 1000).toFixed(1)),
      memoryUsage: process.memoryUsage(),
    });
  });

  // Observability: Readiness Probe
  router.get("/healthz", async (req, res) => {
    const checks = {};
    let isReady = true;

    try {
      if (redisClientWrapper.isConnected()) {
        checks.redis = "healthy";
      } else {
        checks.redis = "mock_fallback_active";
      }
    } catch (err) {
      checks.redis = `unhealthy: ${err.message}`;
      isReady = false;
    }

    res.status(isReady ? 200 : 503).json({
      status: isReady ? "ready" : "degraded",
      service: "session-router",
      checks,
      timestamp: new Date().toISOString(),
    });
  });

  // Telehealth Session Lifecycle APIs
  router.post("/api/v1/sessions", async (req, res) => {
    try {
      const { patientId, doctorId, metadata } = req.body;
      if (!patientId || !doctorId) {
        return res.status(400).json({
          error: "Missing required fields: 'patientId' and 'doctorId'",
        });
      }

      const session = await sessionStore.createSession({ patientId, doctorId, metadata });
      res.status(201).json({
        status: "created",
        session,
      });
    } catch (err) {
      logger.error({ error: err.message }, "Error creating session");
      res.status(500).json({ error: "Failed to create session" });
    }
  });

  router.get("/api/v1/sessions/:id", async (req, res) => {
    try {
      const session = await sessionStore.getSession(req.params.id);
      if (!session) {
        return res.status(404).json({ error: "Session not found" });
      }
      res.status(200).json({ session });
    } catch (err) {
      logger.error({ error: err.message }, "Error fetching session");
      res.status(500).json({ error: "Failed to fetch session" });
    }
  });

  router.post("/api/v1/sessions/:id/terminate", async (req, res) => {
    try {
      const { reason } = req.body || {};
      const session = await sessionStore.terminateSession(req.params.id, reason);
      if (!session) {
        return res.status(404).json({ error: "Session not found" });
      }
      res.status(200).json({
        status: "terminated",
        session,
      });
    } catch (err) {
      logger.error({ error: err.message }, "Error terminating session");
      res.status(500).json({ error: "Failed to terminate session" });
    }
  });

  router.get("/", (req, res) => {
    res.json({
      service: "session-router",
      description: "WebRTC signaling metadata router and session state tracker",
      endpoints: [
        "/api/v1/sessions",
        "/ws/signal",
        "/healthz",
        "/livez",
        "/metrics",
      ],
    });
  });

  return router;
}
