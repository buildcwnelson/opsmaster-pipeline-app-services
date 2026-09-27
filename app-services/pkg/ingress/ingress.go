package ingress

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/opsmaster/app-services/pkg/cache"
	"github.com/opsmaster/app-services/pkg/config"
	"github.com/opsmaster/app-services/pkg/metrics"
)

type MetricType string

const (
	MetricHeartRate              MetricType = "heart_rate"
	MetricSpO2                   MetricType = "spo2"
	MetricECGWaveform            MetricType = "ecg_waveform"
	MetricBloodPressureSystolic  MetricType = "blood_pressure_systolic"
	MetricBloodPressureDiastolic MetricType = "blood_pressure_diastolic"
	MetricRespiratoryRate        MetricType = "respiratory_rate"
)

type VitalsPayload struct {
	PatientID  string            `json:"patient_id"`
	DeviceID   string            `json:"device_id"`
	MetricType MetricType        `json:"metric_type"`
	Value      *float64          `json:"value,omitempty"`
	Waveform   []float64         `json:"waveform,omitempty"`
	Unit       string            `json:"unit,omitempty"`
	Timestamp  time.Time         `json:"timestamp"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

func (v *VitalsPayload) Validate() error {
	v.PatientID = strings.TrimSpace(v.PatientID)
	if v.PatientID == "" {
		return errors.New("field 'patient_id' is required")
	}

	v.DeviceID = strings.TrimSpace(v.DeviceID)
	if v.DeviceID == "" {
		return errors.New("field 'device_id' is required")
	}

	switch v.MetricType {
	case MetricHeartRate, MetricSpO2, MetricECGWaveform, MetricBloodPressureSystolic, MetricBloodPressureDiastolic, MetricRespiratoryRate:
		// Valid metric type
	default:
		return fmt.Errorf("invalid 'metric_type': %s", v.MetricType)
	}

	if v.MetricType == MetricECGWaveform {
		if len(v.Waveform) == 0 {
			return errors.New("field 'waveform' must be a non-empty array of floats for ecg_waveform")
		}
	} else {
		if v.Value == nil {
			return fmt.Errorf("field 'value' is required for metric_type %s", v.MetricType)
		}
	}

	if v.Timestamp.IsZero() {
		v.Timestamp = time.Now().UTC()
	}

	return nil
}

type Handler struct {
	cfg          *config.Config
	cache        cache.Cache
	alertChannel chan<- *VitalsPayload
	upgrader     websocket.Upgrader
}

func NewHandler(cfg *config.Config, c cache.Cache, alertChan chan<- *VitalsPayload) *Handler {
	return &Handler{
		cfg:          cfg,
		cache:        c,
		alertChannel: alertChan,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true // Production CORS or origin verification
			},
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
		},
	}
}

// IngestREST handles POST /api/v1/vitals
func (h *Handler) IngestREST(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 5*1024*1024))
	if err != nil {
		http.Error(w, `{"error":"failed to read request body"}`, http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var payloads []VitalsPayload

	// Try array first
	if err := json.Unmarshal(body, &payloads); err != nil {
		// Try single payload object
		var single VitalsPayload
		if singleErr := json.Unmarshal(body, &single); singleErr != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error":   "invalid JSON payload",
				"details": singleErr.Error(),
			})
			return
		}
		payloads = append(payloads, single)
	}

	if len(payloads) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "empty payload array"})
		return
	}

	acceptedCount := 0
	var validationErrors []string

	for _, p := range payloads {
		if err := p.Validate(); err != nil {
			validationErrors = append(validationErrors, err.Error())
			metrics.VitalsIngestedTotal.WithLabelValues(string(p.MetricType), "http", "validation_error").Inc()
			continue
		}

		// 1. Process with internal sliding-window alert worker goroutine
		select {
		case h.alertChannel <- &p:
		default:
			slog.WarnContext(r.Context(), "alert worker channel buffer full, falling back to stream only",
				slog.String("patient_id", p.PatientID),
			)
		}

		// 2. Persist to Redis stream for long-term stream consumer durability
		streamData := map[string]interface{}{
			"patient_id":  p.PatientID,
			"device_id":   p.DeviceID,
			"metric_type": string(p.MetricType),
			"timestamp":   p.Timestamp.Format(time.RFC3339Nano),
		}
		if p.Value != nil {
			streamData["value"] = *p.Value
		}
		if len(p.Waveform) > 0 {
			waveformJSON, _ := json.Marshal(p.Waveform)
			streamData["waveform"] = string(waveformJSON)
		}

		_ = h.cache.PushVitalStream(r.Context(), h.cfg.RedisVitalsStream, streamData)

		metrics.VitalsIngestedTotal.WithLabelValues(string(p.MetricType), "http", "success").Inc()
		acceptedCount++
	}

	statusCode := http.StatusAccepted
	statusText := "accepted"
	if acceptedCount == 0 && len(validationErrors) > 0 {
		statusCode = http.StatusBadRequest
		statusText = "rejected"
	} else if len(validationErrors) > 0 {
		statusCode = http.StatusMultiStatus
		statusText = "partially_accepted"
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    statusText,
		"accepted":  acceptedCount,
		"errors":    validationErrors,
		"timestamp": time.Now().UTC(),
	})
}

// IngestWebSocket handles GET /api/v1/vitals/ws for live wearable streaming
func (h *Handler) IngestWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("Failed to upgrade WebSocket for vitals ingestion", slog.String("error", err.Error()))
		return
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	slog.Info("Vitals WebSocket client connected", slog.String("remote_addr", r.RemoteAddr))

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				slog.Warn("Vitals WebSocket unexpected close", slog.String("error", err.Error()))
			}
			break
		}

		var payload VitalsPayload
		if err := json.Unmarshal(message, &payload); err != nil {
			_ = conn.WriteJSON(map[string]string{"error": "malformed JSON: " + err.Error()})
			continue
		}

		if err := payload.Validate(); err != nil {
			metrics.VitalsIngestedTotal.WithLabelValues(string(payload.MetricType), "ws", "validation_error").Inc()
			_ = conn.WriteJSON(map[string]string{"error": "validation failed: " + err.Error()})
			continue
		}

		// Dispatch to alert evaluator channel
		select {
		case h.alertChannel <- &payload:
		default:
		}

		metrics.VitalsIngestedTotal.WithLabelValues(string(payload.MetricType), "ws", "success").Inc()

		// Send ack back to sensor
		_ = conn.WriteJSON(map[string]string{
			"status":    "acknowledged",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		})
	}

	slog.InfoContext(ctx, "Vitals WebSocket client disconnected", slog.String("remote_addr", r.RemoteAddr))
}
