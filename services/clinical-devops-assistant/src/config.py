import os
from pydantic import BaseModel, Field

class Settings(BaseModel):
    service_name: str = "clinical-devops-assistant"
    port: int = Field(default_factory=lambda: int(os.getenv("PORT", "8083")))
    
    # PostgreSQL Database URL
    database_url: str = Field(default_factory=lambda: os.getenv(
        "DATABASE_URL",
        "postgresql+asyncpg://postgres:postgres@localhost:5432/clinical_db"
    ))
    
    # Upstream service URLs for cluster diagnostics
    vitals_ingress_url: str = Field(default_factory=lambda: os.getenv("VITALS_INGRESS_URL", "http://localhost:8080"))
    alert_engine_url: str = Field(default_factory=lambda: os.getenv("ALERT_ENGINE_URL", "http://localhost:8081"))
    session_router_url: str = Field(default_factory=lambda: os.getenv("SESSION_ROUTER_URL", "http://localhost:8082"))
    
    shutdown_timeout_seconds: int = Field(default_factory=lambda: int(os.getenv("SHUTDOWN_TIMEOUT_SECONDS", "10")))
    enable_sqlite_fallback: bool = Field(default_factory=lambda: os.getenv("ENABLE_SQLITE_FALLBACK", "true").lower() == "true")

settings = Settings()
