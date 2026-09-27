-- Clinical Database Schema Initialization for OpsMaster Pipeline
-- PostgreSQL 16+

CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- Patients Table
CREATE TABLE IF NOT EXISTS patients (
    id VARCHAR(64) PRIMARY KEY,
    mrn VARCHAR(64) UNIQUE NOT NULL,
    first_name VARCHAR(100) NOT NULL,
    last_name VARCHAR(100) NOT NULL,
    date_of_birth DATE NOT NULL,
    gender VARCHAR(20) DEFAULT 'unspecified',
    room_number VARCHAR(32),
    attending_physician VARCHAR(128),
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_patients_mrn ON patients(mrn);

-- Telemetry Anomaly Alerts Table
CREATE TABLE IF NOT EXISTS telemetry_alerts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    patient_id VARCHAR(64) NOT NULL REFERENCES patients(id) ON DELETE CASCADE,
    device_id VARCHAR(64) NOT NULL,
    anomaly_type VARCHAR(64) NOT NULL,
    severity VARCHAR(32) NOT NULL,
    current_value DOUBLE PRECISION NOT NULL,
    threshold_value DOUBLE PRECISION NOT NULL,
    unit VARCHAR(32) NOT NULL,
    description TEXT NOT NULL,
    detected_at TIMESTAMPTZ NOT NULL,
    resolved BOOLEAN DEFAULT FALSE,
    resolved_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_telemetry_alerts_patient ON telemetry_alerts(patient_id, detected_at DESC);
CREATE INDEX IF NOT EXISTS idx_telemetry_alerts_severity ON telemetry_alerts(severity, detected_at DESC);
CREATE INDEX IF NOT EXISTS idx_telemetry_alerts_anomaly ON telemetry_alerts(anomaly_type, detected_at DESC);

-- Clinical DevOps Diagnostic Log Records
CREATE TABLE IF NOT EXISTS diagnostic_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    service_name VARCHAR(64) NOT NULL,
    log_level VARCHAR(32) NOT NULL,
    message TEXT NOT NULL,
    metadata JSONB DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_diagnostic_logs_created ON diagnostic_logs(created_at DESC);

-- Seed Seed Initial Patient Records
INSERT INTO patients (id, mrn, first_name, last_name, date_of_birth, gender, room_number, attending_physician)
VALUES 
    ('pat-001', 'MRN-88201', 'Eleanor', 'Vance', '1965-04-12', 'female', 'ICU-301', 'Dr. Sarah Lin, MD'),
    ('pat-002', 'MRN-88202', 'Marcus', 'Chen', '1978-09-23', 'male', 'CCU-104', 'Dr. Robert Patel, MD'),
    ('pat-003', 'MRN-88203', 'Sophia', 'Rodriguez', '1989-11-05', 'female', 'PACU-205', 'Dr. Sarah Lin, MD')
ON CONFLICT (id) DO NOTHING;
