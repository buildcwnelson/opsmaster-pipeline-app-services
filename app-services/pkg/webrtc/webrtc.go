package webrtc

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/opsmaster/app-services/pkg/cache"
	"github.com/opsmaster/app-services/pkg/metrics"
)

type SignalingMessage struct {
	Type         string          `json:"type"` // join, joined, offer, answer, ice-candidate, leave, peer-joined, peer-left
	SessionID    string          `json:"session_id"`
	PeerID       string          `json:"peer_id"`
	TargetPeerID string          `json:"target_peer_id,omitempty"`
	Payload      json.RawMessage `json:"payload,omitempty"`
}

type Router struct {
	cache    cache.Cache
	upgrader websocket.Upgrader

	mu    sync.RWMutex
	rooms map[string]map[string]*websocket.Conn // sessionID -> peerID -> conn
}

func NewRouter(c cache.Cache) *Router {
	return &Router{
		cache: c,
		upgrader: websocket.Upgrader{
			CheckOrigin:     func(r *http.Request) bool { return true },
			ReadBufferSize:  2048,
			WriteBufferSize: 2048,
		},
		rooms: make(map[string]map[string]*websocket.Conn),
	}
}

// RegisterRoutes attaches WebRTC REST and WebSocket endpoints
func (rt *Router) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/telehealth/sessions", rt.handleSessions)
	mux.HandleFunc("/api/v1/telehealth/sessions/", rt.handleSessionByID)
	mux.HandleFunc("/api/v1/telehealth/ws", rt.handleWebSocketSignaling)
}

func (rt *Router) handleSessions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var req struct {
			PatientID string            `json:"patient_id"`
			DoctorID  string            `json:"doctor_id"`
			Metadata  map[string]string `json:"metadata"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
			return
		}

		if req.PatientID == "" || req.DoctorID == "" {
			http.Error(w, `{"error":"patient_id and doctor_id are required"}`, http.StatusBadRequest)
			return
		}

		sessionID := generateID(12)
		now := time.Now().UTC()
		session := &cache.TelehealthSession{
			ID:        sessionID,
			PatientID: req.PatientID,
			DoctorID:  req.DoctorID,
			Status:    "WAITING",
			CreatedAt: now,
			UpdatedAt: now,
			Peers:     make(map[string]cache.PeerConnection),
			Metadata:  req.Metadata,
		}

		if err := rt.cache.SaveSession(r.Context(), session); err != nil {
			http.Error(w, `{"error":"failed to create session"}`, http.StatusInternalServerError)
			return
		}

		metrics.ActiveSessionsGauge.Inc()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(session)

	case http.MethodGet:
		sessions, err := rt.cache.ListSessions(r.Context())
		if err != nil {
			http.Error(w, `{"error":"failed to list sessions"}`, http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"sessions": sessions,
			"count":    len(sessions),
		})

	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (rt *Router) handleSessionByID(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/telehealth/sessions/")
	parts := strings.Split(path, "/")
	sessionID := parts[0]

	if sessionID == "" {
		http.Error(w, `{"error":"session id required"}`, http.StatusBadRequest)
		return
	}

	session, err := rt.cache.GetSession(r.Context(), sessionID)
	if err != nil {
		http.Error(w, `{"error":"failed to load session"}`, http.StatusInternalServerError)
		return
	}
	if session == nil {
		http.Error(w, `{"error":"session not found"}`, http.StatusNotFound)
		return
	}

	// Handle /terminate
	if len(parts) > 1 && parts[1] == "terminate" && r.Method == http.MethodPost {
		var req struct {
			Reason string `json:"reason"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Reason == "" {
			req.Reason = "normal_completion"
		}

		now := time.Now().UTC()
		session.Status = "TERMINATED"
		session.EndedAt = &now
		session.TerminationReason = req.Reason
		session.UpdatedAt = now

		_ = rt.cache.SaveSession(r.Context(), session)
		metrics.ActiveSessionsGauge.Dec()

		// Disconnect peers from room
		rt.mu.Lock()
		if room, ok := rt.rooms[sessionID]; ok {
			for _, conn := range room {
				_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "Session terminated"))
				_ = conn.Close()
			}
			delete(rt.rooms, sessionID)
		}
		rt.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(session)
		return
	}

	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(session)
		return
	}

	http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
}

func (rt *Router) handleWebSocketSignaling(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("sessionId")
	peerID := r.URL.Query().Get("peerId")
	role := r.URL.Query().Get("role")

	if sessionID == "" || peerID == "" {
		http.Error(w, `{"error":"sessionId and peerId query parameters required"}`, http.StatusBadRequest)
		return
	}

	conn, err := rt.upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("Failed to upgrade WebRTC signaling WebSocket", slog.String("error", err.Error()))
		return
	}
	defer conn.Close()

	metrics.ActivePeersGauge.Inc()
	defer metrics.ActivePeersGauge.Dec()

	// Register peer in room
	rt.mu.Lock()
	if _, ok := rt.rooms[sessionID]; !ok {
		rt.rooms[sessionID] = make(map[string]*websocket.Conn)
	}
	rt.rooms[sessionID][peerID] = conn
	rt.mu.Unlock()

	// Update session state in Redis
	session, _ := rt.cache.GetSession(r.Context(), sessionID)
	if session != nil {
		if session.Peers == nil {
			session.Peers = make(map[string]cache.PeerConnection)
		}
		session.Peers[peerID] = cache.PeerConnection{Role: role, JoinedAt: time.Now().UTC()}
		if len(session.Peers) >= 2 {
			session.Status = "CONNECTED"
		}
		_ = rt.cache.SaveSession(r.Context(), session)
	}

	// Broadcast peer-joined to others
	rt.broadcastToRoom(sessionID, peerID, SignalingMessage{
		Type:      "peer-joined",
		SessionID: sessionID,
		PeerID:    peerID,
	})

	slog.Info("WebRTC peer connected to telehealth room",
		slog.String("session_id", sessionID),
		slog.String("peer_id", peerID),
		slog.String("role", role),
	)

	// Signaling loop
	for {
		_, msgBytes, err := conn.ReadMessage()
		if err != nil {
			break
		}

		var msg SignalingMessage
		if err := json.Unmarshal(msgBytes, &msg); err != nil {
			continue
		}

		msg.SessionID = sessionID
		msg.PeerID = peerID

		metrics.SignalingMessagesTotal.WithLabelValues(msg.Type, "forwarded").Inc()

		if msg.TargetPeerID != "" {
			rt.sendToPeer(sessionID, msg.TargetPeerID, msg)
		} else {
			rt.broadcastToRoom(sessionID, peerID, msg)
		}
	}

	// Peer disconnect cleanup
	rt.mu.Lock()
	if room, ok := rt.rooms[sessionID]; ok {
		delete(room, peerID)
		if len(room) == 0 {
			delete(rt.rooms, sessionID)
		}
	}
	rt.mu.Unlock()

	// Update Redis state
	if session, _ := rt.cache.GetSession(r.Context(), sessionID); session != nil {
		delete(session.Peers, peerID)
		if len(session.Peers) < 2 && session.Status == "CONNECTED" {
			session.Status = "WAITING"
		}
		_ = rt.cache.SaveSession(r.Context(), session)
	}

	rt.broadcastToRoom(sessionID, peerID, SignalingMessage{
		Type:      "peer-left",
		SessionID: sessionID,
		PeerID:    peerID,
	})

	slog.Info("WebRTC peer left telehealth room",
		slog.String("session_id", sessionID),
		slog.String("peer_id", peerID),
	)
}

func (rt *Router) sendToPeer(sessionID, targetPeerID string, msg SignalingMessage) {
	rt.mu.RLock()
	defer rt.mu.RUnlock()

	if room, ok := rt.rooms[sessionID]; ok {
		if conn, ok := room[targetPeerID]; ok {
			_ = conn.WriteJSON(msg)
		}
	}
}

func (rt *Router) broadcastToRoom(sessionID, senderPeerID string, msg SignalingMessage) {
	rt.mu.RLock()
	defer rt.mu.RUnlock()

	if room, ok := rt.rooms[sessionID]; ok {
		for peerID, conn := range room {
			if peerID != senderPeerID {
				_ = conn.WriteJSON(msg)
			}
		}
	}
}

func generateID(length int) string {
	b := make([]byte, length/2)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
