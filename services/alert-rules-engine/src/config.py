import os
from pydantic import BaseModel, Field

class Settings(BaseModel):
    service_name: str = "alert-rules-engine"
    http_port: int = Field(default_factory=lambda: int(os.getenv("HTTP_PORT", "8081")))
    
    redis_addr: str = Field(default_factory=lambda: os.getenv("REDIS_ADDR", "localhost:6379"))
    redis_password: str = Field(default_factory=lambda: os.getenv("REDIS_PASSWORD", ""))
    redis_stream: str = Field(default_factory=lambda: os.getenv("REDIS_STREAM", "vitals:stream"))
    redis_consumer_group: str = Field(default_factory=lambda: os.getenv("REDIS_CONSUMER_GROUP", "alert_rules_engine_group"))
    redis_consumer_name: str = Field(default_factory=lambda: os.getenv("REDIS_CONSUMER_NAME", "worker-1"))
    
    webhook_url: str = Field(default_factory=lambda: os.getenv("WEBHOOK_URL", "http://localhost:8083/api/v1/alerts/webhook"))
    database_url: str = Field(default_factory=lambda: os.getenv("DATABASE_URL", "postgresql://postgres:postgres@localhost:5432/clinical_db"))
    
    sliding_window_seconds: int = Field(default_factory=lambda: int(os.getenv("SLIDING_WINDOW_SECONDS", "30")))
    sliding_window_min_samples: int = Field(default_factory=lambda: int(os.getenv("SLIDING_WINDOW_MIN_SAMPLES", "2")))
    
    # Thresholds
    tachycardia_bpm: float = Field(default_factory=lambda: float(os.getenv("THRESHOLD_TACHYCARDIA_BPM", "140.0")))
    bradycardia_bpm: float = Field(default_factory=lambda: float(os.getenv("THRESHOLD_BRADYCARDIA_BPM", "45.0")))
    hypoxemia_spo2: float = Field(default_factory=lambda: float(os.getenv("THRESHOLD_HYPOXEMIA_SPO2", "90.0")))
    critical_hypoxemia_spo2: float = Field(default_factory=lambda: float(os.getenv("THRESHOLD_CRITICAL_HYPOXEMIA_SPO2", "85.0")))
    systolic_high: float = Field(default_factory=lambda: float(os.getenv("THRESHOLD_SYSTOLIC_HIGH", "180.0")))
    
    shutdown_timeout_seconds: int = Field(default_factory=lambda: int(os.getenv("SHUTDOWN_TIMEOUT_SECONDS", "10")))
    enable_mock_fallback: bool = Field(default_factory=lambda: os.getenv("ENABLE_MOCK_FALLBACK", "true").lower() == "true")

settings = Settings()
