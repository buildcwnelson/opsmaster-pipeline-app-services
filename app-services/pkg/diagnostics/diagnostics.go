package diagnostics

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"time"

	"github.com/opsmaster/app-services/pkg/cache"
	"github.com/opsmaster/app-services/pkg/database"
)

type DiagnosticFinding struct {
	FindingID         string                 `json:"finding_id"`
	Component         string                 `json:"component"`
	Severity          string                 `json:"severity"` // INFO, WARNING, CRITICAL
	Category          string                 `json:"category"`
	Summary           string                 `json:"summary"`
	RecommendedAction string                 `json:"recommended_action"`
	Evidence          map[string]interface{} `json:"evidence"`
}

type DiagnosticReport struct {
	ReportID        string              `json:"report_id"`
	GeneratedAt     time.Time           `json:"generated_at"`
	OverallStatus   string              `json:"overall_status"` // HEALTHY, DEGRADED, CRITICAL
	SystemStats     SystemRuntimeStats  `json:"system_stats"`
	DependencyCheck map[string]string   `json:"dependency_checks"`
	RecentAlerts    []database.TelemetryAlert `json:"recent_alerts"`
	Findings        []DiagnosticFinding `json:"findings"`
}

type SystemRuntimeStats struct {
	NumGoroutines int    `json:"num_goroutines"`
	NumCPU        int    `json:"num_cpu"`
	AllocMB       string `json:"alloc_mb"`
	TotalAllocMB  string `json:"total_alloc_mb"`
	SysMB         string `json:"sys_mb"`
	NumGC         uint32 `json:"num_gc"`
}

type Handler struct {
	db    database.DB
	cache cache.Cache
}

func NewHandler(db database.DB, c cache.Cache) *Handler {
	return &Handler{
		db:    db,
		cache: c,
	}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/admin/diagnose", h.handleDiagnose)
	mux.HandleFunc("/api/v1/admin/logs", h.handleLogs)
}

func (h *Handler) handleDiagnose(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()
	findings := make([]DiagnosticFinding, 0)
	depChecks := make(map[string]string)
	isDegraded := false

	// 1. Check PostgreSQL
	if err := h.db.Ping(ctx); err != nil {
		depChecks["postgres"] = "unhealthy: " + err.Error()
		isDegraded = true
		findings = append(findings, DiagnosticFinding{
			FindingID:         "find-" + generateID(6),
			Component:         "postgres",
			Severity:          "CRITICAL",
			Category:          "DATABASE_DISCONNECTED",
			Summary:           "PostgreSQL primary database ping failed",
			RecommendedAction: "Check database container health, connection limits, and credentials.",
			Evidence:          map[string]interface{}{"error": err.Error()},
		})
	} else {
		depChecks["postgres"] = "healthy"
	}

	// 2. Check Redis
	if err := h.cache.Ping(ctx); err != nil {
		depChecks["redis"] = "unhealthy: " + err.Error()
		isDegraded = true
		findings = append(findings, DiagnosticFinding{
			FindingID:         "find-" + generateID(6),
			Component:         "redis",
			Severity:          "CRITICAL",
			Category:          "CACHE_QUEUE_DISCONNECTED",
			Summary:           "Redis cache & stream queue ping failed",
			RecommendedAction: "Verify Redis memory limits and cluster status.",
			Evidence:          map[string]interface{}{"error": err.Error()},
		})
	} else {
		depChecks["redis"] = "healthy"
	}

	// 3. Query Recent Alerts
	recentAlerts, _ := h.db.ListAlerts(ctx, "", 10)
	if len(recentAlerts) > 5 {
		findings = append(findings, DiagnosticFinding{
			FindingID:         "find-" + generateID(6),
			Component:         "alerts-engine",
			Severity:          "WARNING",
			Category:          "ELEVATED_ANOMALY_RATE",
			Summary:           fmt.Sprintf("Elevated anomaly count: %d medical alerts detected in recent window", len(recentAlerts)),
			RecommendedAction: "Review bedside patient telemetry streams for telemetry artifacts or patient distress.",
			Evidence:          map[string]interface{}{"alert_count": len(recentAlerts)},
		})
	}

	// 4. Runtime Stats
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	stats := SystemRuntimeStats{
		NumGoroutines: runtime.NumGoroutine(),
		NumCPU:        runtime.NumCPU(),
		AllocMB:       fmt.Sprintf("%.2f MB", float64(m.Alloc)/(1024*1024)),
		TotalAllocMB:  fmt.Sprintf("%.2f MB", float64(m.TotalAlloc)/(1024*1024)),
		SysMB:         fmt.Sprintf("%.2f MB", float64(m.Sys)/(1024*1024)),
		NumGC:         m.NumGC,
	}

	overallStatus := "HEALTHY"
	if isDegraded {
		overallStatus = "CRITICAL"
	} else if len(findings) > 0 {
		overallStatus = "DEGRADED"
	}

	report := DiagnosticReport{
		ReportID:        "diag-" + generateID(8),
		GeneratedAt:     time.Now().UTC(),
		OverallStatus:   overallStatus,
		SystemStats:     stats,
		DependencyCheck: depChecks,
		RecentAlerts:    recentAlerts,
		Findings:        findings,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(report)
}

func (h *Handler) handleLogs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	switch r.Method {
	case http.MethodGet:
		logs, err := h.db.ListDiagnosticLogs(ctx, 50)
		if err != nil {
			http.Error(w, `{"error":"failed to fetch logs"}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"logs":  logs,
			"count": len(logs),
		})

	case http.MethodPost:
		var entry database.DiagnosticLogEntry
		if err := json.NewDecoder(r.Body).Decode(&entry); err != nil {
			http.Error(w, `{"error":"invalid log JSON"}`, http.StatusBadRequest)
			return
		}

		entry.ID = generateID(16)
		entry.CreatedAt = time.Now().UTC()
		if entry.ServiceName == "" {
			entry.ServiceName = "app-engine"
		}
		if entry.LogLevel == "" {
			entry.LogLevel = "INFO"
		}

		if err := h.db.SaveDiagnosticLog(ctx, &entry); err != nil {
			http.Error(w, `{"error":"failed to record log"}`, http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(entry)

	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func generateID(length int) string {
	b := make([]byte, length/2)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
