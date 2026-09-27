from datetime import datetime
from typing import Optional, List, Dict, Any
from pydantic import BaseModel, Field

class TelemetryRecord(BaseModel):
    patient_id: str
    device_id: str
    metric_type: str
    value: Optional[float] = None
    waveform: Optional[List[float]] = None
    unit: Optional[str] = None
    timestamp: datetime = Field(default_factory=datetime.utcnow)
    metadata: Optional[Dict[str, Any]] = None

class MedicalAnomaly(BaseModel):
    patient_id: str
    device_id: str
    anomaly_type: str  # TACHYCARDIA, BRADYCARDIA, HYPOXEMIA, CRITICAL_HYPOXEMIA, HYPERTENSION, ECG_ARRHYTHMIA
    severity: str      # INFO, WARNING, CRITICAL
    current_value: float
    threshold_value: float
    unit: str
    window_samples: int
    window_avg: float
    description: str
    detected_at: datetime = Field(default_factory=datetime.utcnow)

class AlertNotification(BaseModel):
    alert_id: str
    anomaly: MedicalAnomaly
    dispatched_at: datetime = Field(default_factory=datetime.utcnow)
    channels: List[str] = ["database", "webhook", "redis_pubsub"]
