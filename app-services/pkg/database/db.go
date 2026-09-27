package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	_ "github.com/lib/pq"
	"github.com/opsmaster/app-services/pkg/config"
)

type TelemetryAlert struct {
	ID             string    `json:"id"`
	PatientID      string    `json:"patient_id"`
	DeviceID       string    `json:"device_id"`
	AnomalyType    string    `json:"anomaly_type"`
	Severity       string    `json:"severity"`
	CurrentValue   float64   `json:"current_value"`
	ThresholdValue float64   `json:"threshold_value"`
	Unit           string    `json:"unit"`
	Description    string    `json:"description"`
	DetectedAt     time.Time `json:"detected_at"`
	Resolved       bool      `json:"resolved"`
	CreatedAt      time.Time `json:"created_at"`
}

type DiagnosticLogEntry struct {
	ID          string                 `json:"id"`
	ServiceName string                 `json:"service_name"`
	LogLevel    string                 `json:"log_level"`
	Message     string                 `json:"message"`
	Metadata    map[string]interface{} `json:"metadata"`
	CreatedAt   time.Time              `json:"created_at"`
}

type Patient struct {
	ID                 string    `json:"id"`
	MRN                string    `json:"mrn"`
	FirstName          string    `json:"first_name"`
	LastName           string    `json:"last_name"`
	DateOfBirth        string    `json:"date_of_birth"`
	RoomNumber         string    `json:"room_number"`
	AttendingPhysician string    `json:"attending_physician"`
	CreatedAt          time.Time `json:"created_at"`
}

type DB interface {
	SaveAlert(ctx context.Context, alert *TelemetryAlert) error
	ListAlerts(ctx context.Context, patientID string, limit int) ([]TelemetryAlert, error)
	SaveDiagnosticLog(ctx context.Context, entry *DiagnosticLogEntry) error
	ListDiagnosticLogs(ctx context.Context, limit int) ([]DiagnosticLogEntry, error)
	GetPatient(ctx context.Context, id string) (*Patient, error)
	Ping(ctx context.Context) error
	Close() error
}

type PostgresDB struct {
	db       *sql.DB
	mockMode bool
	mu       sync.RWMutex
	alerts   []TelemetryAlert
	logs     []DiagnosticLogEntry
	patients map[string]Patient
}

func New(cfg *config.Config) (DB, error) {
	db, err := sql.Open("postgres", cfg.DatabaseURL)
	if err != nil {
		if cfg.EnableMockFallback {
			return newMockDB(), nil
		}
		return nil, fmt.Errorf("failed to open postgres connection: %w", err)
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		if cfg.EnableMockFallback {
			slog.Warn("Postgres connection failed, operating with in-memory database fallback",
				slog.String("db_url", cfg.DatabaseURL),
				slog.String("error", err.Error()),
			)
			return newMockDB(), nil
		}
		return nil, fmt.Errorf("failed to ping postgres: %w", err)
	}

	slog.Info("PostgreSQL connection pool verified and ready")
	return &PostgresDB{db: db, mockMode: false}, nil
}

func newMockDB() *PostgresDB {
	p := make(map[string]Patient)
	p["pat-001"] = Patient{ID: "pat-001", MRN: "MRN-88201", FirstName: "Eleanor", LastName: "Vance", RoomNumber: "ICU-301"}
	p["pat-002"] = Patient{ID: "pat-002", MRN: "MRN-88202", FirstName: "Marcus", LastName: "Chen", RoomNumber: "CCU-104"}
	p["pat-003"] = Patient{ID: "pat-003", MRN: "MRN-88203", FirstName: "Sophia", LastName: "Rodriguez", RoomNumber: "PACU-205"}

	return &PostgresDB{
		mockMode: true,
		alerts:   make([]TelemetryAlert, 0, 100),
		logs:     make([]DiagnosticLogEntry, 0, 100),
		patients: p,
	}
}

func (p *PostgresDB) SaveAlert(ctx context.Context, alert *TelemetryAlert) error {
	if p.mockMode {
		p.mu.Lock()
		defer p.mu.Unlock()
		alert.CreatedAt = time.Now().UTC()
		p.alerts = append(p.alerts, *alert)
		return nil
	}

	query := `
		INSERT INTO telemetry_alerts 
		(id, patient_id, device_id, anomaly_type, severity, current_value, threshold_value, unit, description, detected_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`
	_, err := p.db.ExecContext(ctx, query,
		alert.ID,
		alert.PatientID,
		alert.DeviceID,
		alert.AnomalyType,
		alert.Severity,
		alert.CurrentValue,
		alert.ThresholdValue,
		alert.Unit,
		alert.Description,
		alert.DetectedAt,
	)
	return err
}

func (p *PostgresDB) ListAlerts(ctx context.Context, patientID string, limit int) ([]TelemetryAlert, error) {
	if p.mockMode {
		p.mu.RLock()
		defer p.mu.RUnlock()
		res := make([]TelemetryAlert, 0)
		for _, a := range p.alerts {
			if patientID == "" || a.PatientID == patientID {
				res = append(res, a)
			}
			if limit > 0 && len(res) >= limit {
				break
			}
		}
		return res, nil
	}

	query := `
		SELECT id, patient_id, device_id, anomaly_type, severity, current_value, threshold_value, unit, description, detected_at, resolved, created_at
		FROM telemetry_alerts
		WHERE ($1 = '' OR patient_id = $1)
		ORDER BY detected_at DESC
		LIMIT $2
	`
	rows, err := p.db.QueryContext(ctx, query, patientID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var alerts []TelemetryAlert
	for rows.Next() {
		var a TelemetryAlert
		if err := rows.Scan(
			&a.ID, &a.PatientID, &a.DeviceID, &a.AnomalyType, &a.Severity,
			&a.CurrentValue, &a.ThresholdValue, &a.Unit, &a.Description,
			&a.DetectedAt, &a.Resolved, &a.CreatedAt,
		); err != nil {
			return nil, err
		}
		alerts = append(alerts, a)
	}
	return alerts, nil
}

func (p *PostgresDB) SaveDiagnosticLog(ctx context.Context, entry *DiagnosticLogEntry) error {
	metaJSON, _ := json.Marshal(entry.Metadata)

	if p.mockMode {
		p.mu.Lock()
		defer p.mu.Unlock()
		entry.CreatedAt = time.Now().UTC()
		p.logs = append(p.logs, *entry)
		return nil
	}

	query := `
		INSERT INTO diagnostic_logs (id, service_name, log_level, message, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`
	_, err := p.db.ExecContext(ctx, query,
		entry.ID,
		entry.ServiceName,
		entry.LogLevel,
		entry.Message,
		metaJSON,
		entry.CreatedAt,
	)
	return err
}

func (p *PostgresDB) ListDiagnosticLogs(ctx context.Context, limit int) ([]DiagnosticLogEntry, error) {
	if p.mockMode {
		p.mu.RLock()
		defer p.mu.RUnlock()
		count := len(p.logs)
		if limit > 0 && count > limit {
			return p.logs[count-limit:], nil
		}
		return p.logs, nil
	}

	query := `
		SELECT id, service_name, log_level, message, metadata, created_at
		FROM diagnostic_logs
		ORDER BY created_at DESC
		LIMIT $1
	`
	rows, err := p.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []DiagnosticLogEntry
	for rows.Next() {
		var e DiagnosticLogEntry
		var metaRaw []byte
		if err := rows.Scan(&e.ID, &e.ServiceName, &e.LogLevel, &e.Message, &metaRaw, &e.CreatedAt); err != nil {
			return nil, err
		}
		if len(metaRaw) > 0 {
			_ = json.Unmarshal(metaRaw, &e.Metadata)
		}
		entries = append(entries, e)
	}
	return entries, nil
}

func (p *PostgresDB) GetPatient(ctx context.Context, id string) (*Patient, error) {
	if p.mockMode {
		p.mu.RLock()
		defer p.mu.RUnlock()
		if pat, ok := p.patients[id]; ok {
			return &pat, nil
		}
		return nil, sql.ErrNoRows
	}

	query := `SELECT id, mrn, first_name, last_name, COALESCE(room_number, ''), COALESCE(attending_physician, ''), created_at FROM patients WHERE id = $1`
	var pat Patient
	err := p.db.QueryRowContext(ctx, query, id).Scan(
		&pat.ID, &pat.MRN, &pat.FirstName, &pat.LastName, &pat.RoomNumber, &pat.AttendingPhysician, &pat.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &pat, nil
}

func (p *PostgresDB) Ping(ctx context.Context) error {
	if p.mockMode {
		return nil
	}
	return p.db.PingContext(ctx)
}

func (p *PostgresDB) Close() error {
	if p.db != nil {
		return p.db.Close()
	}
	return nil
}
