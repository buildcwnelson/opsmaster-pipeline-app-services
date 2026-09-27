from datetime import datetime, timezone
from typing import List, Optional, Dict, Any
from pydantic import BaseModel, Field

class DiagnosticFinding(BaseModel):
    finding_id: str
    component: str
    severity: str  # INFO, WARNING, CRITICAL
    category: str  # STREAM_DROP, INGESTION_ERROR, WEBRTC_CONNECTIVITY, LATENCY_SPIKE
    summary: str
    recommended_action: str
    evidence: Dict[str, Any]

class DiagnosticReport(BaseModel):
    report_id: str
    generated_at: datetime = Field(default_factory=lambda: datetime.now(timezone.utc))
    overall_health: str  # HEALTHY, DEGRADED, CRITICAL
    analyzed_window_minutes: int
    findings: List[DiagnosticFinding]
    summary_metrics: Dict[str, Any]

class PostMortemCreate(BaseModel):
    incident_title: str
    incident_severity: str = "P1"  # P0, P1, P2, P3
    summary: str
    root_cause: str
    affected_services: List[str]
    impact_description: str
    start_time: datetime
    resolved_at: Optional[datetime] = None
    action_items: List[str] = []
    author: str = "Cluster Operator"

class PostMortemResponse(PostMortemCreate):
    id: str
    created_at: datetime
    updated_at: datetime

class ServiceHealthItem(BaseModel):
    service_name: str
    url: str
    status: str
    latency_ms: Optional[float] = None
    checks: Optional[Dict[str, Any]] = None
    error: Optional[str] = None

class ClusterHealthResponse(BaseModel):
    cluster_status: str
    timestamp: datetime = Field(default_factory=lambda: datetime.now(timezone.utc))
    services: List[ServiceHealthItem]
