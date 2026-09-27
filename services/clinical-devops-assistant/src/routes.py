import time
from typing import List, Optional
from fastapi import APIRouter, HTTPException, Query, status
from fastapi.responses import JSONResponse

from .database import db_manager, PostMortemRecord
from .diagnosis import diagnostic_engine
from .logger import setup_logger
from .metrics import DIAGNOSES_REQUESTED_TOTAL, POST_MORTEMS_CREATED_TOTAL
from .models import (
    DiagnosticReport,
    PostMortemCreate,
    PostMortemResponse,
    ClusterHealthResponse,
    ServiceHealthItem,
)

logger = setup_logger()
router = APIRouter()
start_time = time.time()

# Observability endpoints
@router.get("/livez", tags=["Observability"])
async def livez():
    return {
        "status": "alive",
        "service": "clinical-devops-assistant",
        "uptime_seconds": round(time.time() - start_time, 2),
    }

@router.get("/healthz", tags=["Observability"])
async def healthz():
    db_ok = await db_manager.check_health()
    status_code = status.HTTP_200_OK if db_ok else status.HTTP_503_SERVICE_UNAVAILABLE
    return JSONResponse(
        content={
            "status": "ready" if db_ok else "degraded",
            "service": "clinical-devops-assistant",
            "checks": {
                "database": "connected" if db_ok else "disconnected",
            },
            "uptime_seconds": round(time.time() - start_time, 2),
        },
        status_code=status_code,
    )

# Diagnostic APIs
@router.get("/api/v1/diagnose", response_model=DiagnosticReport, tags=["Diagnostics"])
@router.post("/api/v1/diagnose", response_model=DiagnosticReport, tags=["Diagnostics"])
async def run_diagnostics(window_minutes: int = Query(default=15, ge=1, le=1440)):
    try:
        report = await diagnostic_engine.run_diagnostics(window_minutes=window_minutes)
        DIAGNOSES_REQUESTED_TOTAL.labels(status="success").inc()
        return report
    except Exception as e:
        logger.error(f"Error during cluster diagnostics execution: {e}", exc_info=True)
        DIAGNOSES_REQUESTED_TOTAL.labels(status="error").inc()
        raise HTTPException(status_code=500, detail=f"Diagnostic evaluation failed: {str(e)}")

# Post-Mortem Incident Logging APIs
@router.post(
    "/api/v1/post-mortems",
    response_model=PostMortemResponse,
    status_code=status.HTTP_201_CREATED,
    tags=["Post-Mortems"],
)
async def create_post_mortem(data: PostMortemCreate):
    try:
        record = await db_manager.save_post_mortem(data.model_dump())
        POST_MORTEMS_CREATED_TOTAL.labels(severity=data.incident_severity).inc()
        logger.info(
            f"Created post-mortem report '{data.incident_title}' (Severity: {data.incident_severity})",
            extra={"incident_title": data.incident_title, "severity": data.incident_severity}
        )
        return record
    except Exception as e:
        logger.error(f"Failed to record post-mortem: {e}")
        raise HTTPException(status_code=500, detail="Database write failure for post-mortem")

@router.get(
    "/api/v1/post-mortems",
    response_model=List[PostMortemResponse],
    tags=["Post-Mortems"],
)
async def list_post_mortems(severity: Optional[str] = Query(None, description="Filter by severity P0-P3")):
    records = await db_manager.list_post_mortems(severity=severity)
    return records

@router.get("/api/v1/cluster/overview", response_model=ClusterHealthResponse, tags=["Diagnostics"])
async def cluster_overview():
    report = await diagnostic_engine.run_diagnostics(window_minutes=5)
    services: List[ServiceHealthItem] = []
    
    for svc_name, svc_data in report.summary_metrics.items():
        services.append(ServiceHealthItem(
            service_name=svc_name,
            url=svc_data.get("url", ""),
            status="healthy" if svc_data.get("healthy") else "unhealthy",
            latency_ms=svc_data.get("latency_ms"),
            checks=svc_data.get("metrics"),
            error=svc_data.get("error")
        ))
        
    return ClusterHealthResponse(
        cluster_status=report.overall_health,
        services=services
    )
