import Redis from "ioredis";
import { config } from "./config.js";
import { logger } from "./logger.js";

class InMemoryMockRedis {
  constructor() {
    this.store = new Map();
    this.isMock = true;
    logger.warn("Using In-Memory Mock Redis store for session router");
  }

  async ping() {
    return "PONG";
  }

  async get(key) {
    return this.store.get(key) || null;
  }

  async set(key, value, ex, seconds) {
    this.store.set(key, value);
    if (ex === "EX" && seconds) {
      setTimeout(() => this.store.delete(key), seconds * 1000);
    }
    return "OK";
  }

  async del(key) {
    return this.store.delete(key) ? 1 : 0;
  }

  async keys(pattern) {
    return Array.from(this.store.keys());
  }

  async quit() {
    return "OK";
  }
}

export function createRedisClient() {
  let client;
  let isConnected = false;

  try {
    client = new Redis({
      host: config.redis.host,
      port: config.redis.port,
      password: config.redis.password,
      keyPrefix: config.redis.keyPrefix,
      lazyConnect: true,
      maxRetriesPerRequest: 1,
      connectTimeout: 2000,
    });

    client.on("connect", () => {
      isConnected = true;
      logger.info({ host: config.redis.host, port: config.redis.port }, "Connected to Redis for session router");
    });

    client.on("error", (err) => {
      isConnected = false;
      logger.warn({ error: err.message }, "Redis connection error");
    });
  } catch (err) {
    logger.warn({ error: err.message }, "Failed to construct Redis client");
  }

  return {
    async connect() {
      try {
        if (client) {
          await client.connect();
          return client;
        }
      } catch (err) {
        if (config.enableMockFallback) {
          logger.warn("Falling back to In-Memory store for sessions");
          return new InMemoryMockRedis();
        }
        throw err;
      }
      return new InMemoryMockRedis();
    },
    getClient() {
      return client;
    },
    isConnected() {
      return isConnected;
    }
  };
}
