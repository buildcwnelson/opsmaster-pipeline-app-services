import { WebSocketServer, WebSocket } from "ws";
import { logger } from "./logger.js";
import { activePeersGauge, signalingMessagesTotal } from "./metrics.js";

export class SignalingServer {
  constructor(server, sessionStore) {
    this.sessionStore = sessionStore;
    this.wss = new WebSocketServer({ server, path: "/ws/signal" });
    // Map of sessionId -> Map of peerId -> ws client
    this.rooms = new Map();

    this.wss.on("connection", (ws, req) => this.handleConnection(ws, req));
    logger.info("WebRTC Signaling WebSocket server initialized on path /ws/signal");
  }

  handleConnection(ws, req) {
    activePeersGauge.inc();
    let currentSessionId = null;
    let currentPeerId = null;

    ws.isAlive = true;
    ws.on("pong", () => { ws.isAlive = true; });

    ws.on("message", async (data) => {
      try {
        const msg = JSON.parse(data.toString());
        const { type, sessionId, peerId, targetPeerId, payload } = msg;

        signalingMessagesTotal.labels(type || "unknown", "inbound").inc();

        switch (type) {
          case "join": {
            currentSessionId = sessionId;
            currentPeerId = peerId;

            if (!this.rooms.has(sessionId)) {
              this.rooms.set(sessionId, new Map());
            }

            const room = this.rooms.get(sessionId);
            room.set(peerId, ws);

            await this.sessionStore.addPeer(sessionId, peerId, payload?.role);

            logger.info({ sessionId, peerId, role: payload?.role }, "Peer joined session room");

            // Notify sender of join acknowledgment
            ws.send(JSON.stringify({
              type: "joined",
              sessionId,
              peerId,
              peersInRoom: Array.from(room.keys()).filter((id) => id !== peerId),
            }));

            // Notify other peers in room
            this.broadcastToRoom(sessionId, peerId, {
              type: "peer-joined",
              peerId,
              role: payload?.role,
            });
            break;
          }

          case "offer":
          case "answer":
          case "ice-candidate": {
            // Forward signaling metadata to target peer or broadcast to other peers
            if (targetPeerId) {
              this.sendToPeer(sessionId, targetPeerId, {
                type,
                fromPeerId: currentPeerId,
                payload,
              });
            } else {
              this.broadcastToRoom(sessionId, currentPeerId, {
                type,
                fromPeerId: currentPeerId,
                payload,
              });
            }
            signalingMessagesTotal.labels(type, "forwarded").inc();
            break;
          }

          case "leave": {
            await this.handlePeerLeave(sessionId, currentPeerId);
            break;
          }

          default:
            logger.warn({ type }, "Unknown signaling message type received");
        }
      } catch (err) {
        logger.error({ error: err.message }, "Error processing signaling message");
      }
    });

    ws.on("close", async () => {
      activePeersGauge.dec();
      if (currentSessionId && currentPeerId) {
        await this.handlePeerLeave(currentSessionId, currentPeerId);
      }
    });

    ws.on("error", (err) => {
      logger.error({ error: err.message, peerId: currentPeerId }, "WebSocket peer error");
    });
  }

  sendToPeer(sessionId, targetPeerId, message) {
    const room = this.rooms.get(sessionId);
    if (!room) return;
    const client = room.get(targetPeerId);
    if (client && client.readyState === WebSocket.OPEN) {
      client.send(JSON.stringify(message));
    }
  }

  broadcastToRoom(sessionId, senderPeerId, message) {
    const room = this.rooms.get(sessionId);
    if (!room) return;
    for (const [peerId, client] of room.entries()) {
      if (peerId !== senderPeerId && client.readyState === WebSocket.OPEN) {
        client.send(JSON.stringify(message));
      }
    }
  }

  async handlePeerLeave(sessionId, peerId) {
    const room = this.rooms.get(sessionId);
    if (room) {
      room.delete(peerId);
      if (room.size === 0) {
        this.rooms.delete(sessionId);
      }
    }

    await this.sessionStore.removePeer(sessionId, peerId);
    this.broadcastToRoom(sessionId, peerId, {
      type: "peer-left",
      peerId,
    });
    logger.info({ sessionId, peerId }, "Peer left session room");
  }

  async close() {
    logger.info("Closing WebSocket signaling server and disconnecting clients...");
    for (const [sessionId, room] of this.rooms.entries()) {
      for (const [peerId, ws] of room.entries()) {
        if (ws.readyState === WebSocket.OPEN) {
          ws.close(1001, "Server shutting down");
        }
      }
    }
    this.rooms.clear();

    return new Promise((resolve) => {
      this.wss.close(() => {
        logger.info("WebSocket signaling server closed");
        resolve();
      });
    });
  }
}
