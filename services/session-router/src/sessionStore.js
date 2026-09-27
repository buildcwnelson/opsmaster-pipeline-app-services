import { v4 as uuidv4 } from "uuid";
import { config } from "./config.js";
import { activeSessionsGauge } from "./metrics.js";
import { logger } from "./logger.js";

export class SessionStore {
  constructor(redis) {
    this.redis = redis;
  }

  async createSession({ patientId, doctorId, metadata = {} }) {
    const sessionId = uuidv4();
    const now = new Date().toISOString();

    const session = {
      id: sessionId,
      patientId,
      doctorId,
      status: "WAITING", // WAITING, CONNECTED, TERMINATED
      createdAt: now,
      updatedAt: now,
      peers: {}, // { [peerId]: { role, joinedAt } }
      metadata,
    };

    await this.redis.set(
      sessionId,
      JSON.stringify(session),
      "EX",
      config.redis.sessionTTL
    );

    activeSessionsGauge.inc();
    logger.info({ sessionId, patientId, doctorId }, "Created telehealth session");
    return session;
  }

  async getSession(sessionId) {
    const data = await this.redis.get(sessionId);
    if (!data) return null;
    return JSON.parse(data);
  }

  async updateSession(sessionId, session) {
    session.updatedAt = new Date().toISOString();
    await this.redis.set(
      sessionId,
      JSON.stringify(session),
      "EX",
      config.redis.sessionTTL
    );
    return session;
  }

  async addPeer(sessionId, peerId, role) {
    const session = await this.getSession(sessionId);
    if (!session) return null;

    session.peers[peerId] = {
      role: role || "participant",
      joinedAt: new Date().toISOString(),
    };

    if (Object.keys(session.peers).length >= 2) {
      session.status = "CONNECTED";
    }

    return await this.updateSession(sessionId, session);
  }

  async removePeer(sessionId, peerId) {
    const session = await this.getSession(sessionId);
    if (!session) return null;

    delete session.peers[peerId];
    if (Object.keys(session.peers).length < 2 && session.status === "CONNECTED") {
      session.status = "WAITING";
    }

    return await this.updateSession(sessionId, session);
  }

  async terminateSession(sessionId, reason = "normal_completion") {
    const session = await this.getSession(sessionId);
    if (!session) return null;

    session.status = "TERMINATED";
    session.terminationReason = reason;
    session.endedAt = new Date().toISOString();

    await this.updateSession(sessionId, session);
    activeSessionsGauge.dec();
    logger.info({ sessionId, reason }, "Terminated telehealth session");
    return session;
  }
}
