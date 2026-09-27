export const config = {
  serviceName: "session-router",
  port: parseInt(process.env.PORT || "8082", 10),
  redis: {
    host: process.env.REDIS_HOST || (process.env.REDIS_ADDR ? process.env.REDIS_ADDR.split(":")[0] : "localhost"),
    port: parseInt(process.env.REDIS_PORT || (process.env.REDIS_ADDR && process.env.REDIS_ADDR.split(":")[1] ? process.env.REDIS_ADDR.split(":")[1] : "6379"), 10),
    password: process.env.REDIS_PASSWORD || undefined,
    keyPrefix: process.env.REDIS_PREFIX || "opsmaster:session:",
    sessionTTL: parseInt(process.env.SESSION_TTL_SECONDS || "86400", 10), // 24 hours
  },
  shutdownTimeoutMs: parseInt(process.env.SHUTDOWN_TIMEOUT_MS || "10000", 10),
  enableMockFallback: process.env.ENABLE_MOCK_FALLBACK !== "false"
};
