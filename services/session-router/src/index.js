import http from "http";
import express from "express";
import { config } from "./config.js";
import { logger } from "./logger.js";
import { createRedisClient } from "./redisClient.js";
import { SessionStore } from "./sessionStore.js";
import { SignalingServer } from "./signalingServer.js";
import { createRouter } from "./routes.js";

async function bootstrap() {
  logger.info("Starting session-router service...");

  const redisWrapper = createRedisClient();
  const redis = await redisWrapper.connect();

  const sessionStore = new SessionStore(redis);

  const app = express();
  app.use(express.json());

  const router = createRouter(sessionStore, redisWrapper);
  app.use(router);

  const server = http.createServer(app);
  const signalingServer = new SignalingServer(server, sessionStore);

  server.listen(config.port, () => {
    logger.info({ port: config.port }, "session-router listening for HTTP & WebSocket connections");
  });

  // Graceful shutdown logic
  let isShuttingDown = false;

  const handleShutdown = async (signal) => {
    if (isShuttingDown) return;
    isShuttingDown = true;

    logger.info({ signal, timeoutMs: config.shutdownTimeoutMs }, "Received termination signal, starting graceful shutdown...");

    const forceShutdownTimer = setTimeout(() => {
      logger.error("Graceful shutdown timeout exceeded, forcing exit");
      process.exit(1);
    }, config.shutdownTimeoutMs);
    forceShutdownTimer.unref();

    try {
      // 1. Close WebSocket signaling server and disconnect active peers
      await signalingServer.close();

      // 2. Stop accepting new HTTP requests and drain active requests
      await new Promise((resolve, reject) => {
        server.close((err) => {
          if (err) return reject(err);
          logger.info("HTTP server closed all client connections");
          resolve();
        });
      });

      // 3. Disconnect Redis
      await redis.quit();
      logger.info("Disconnected from Redis");

      logger.info("session-router shut down cleanly");
      process.exit(0);
    } catch (err) {
      logger.error({ error: err.message }, "Error during graceful shutdown");
      process.exit(1);
    }
  };

  process.on("SIGTERM", () => handleShutdown("SIGTERM"));
  process.on("SIGINT", () => handleShutdown("SIGINT"));
}

bootstrap().catch((err) => {
  logger.fatal({ error: err.message, stack: err.stack }, "Failed to start session-router");
  process.exit(1);
});
