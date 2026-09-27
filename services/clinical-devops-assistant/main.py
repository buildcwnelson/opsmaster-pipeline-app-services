import uvicorn
from contextlib import asynccontextmanager
from fastapi import FastAPI
from prometheus_fastapi_instrumentator import Instrumentator

from src.config import settings
from src.database import db_manager
from src.diagnosis import diagnostic_engine
from src.logger import setup_logger, StructuredLoggingMiddleware
from src.routes import router

logger = setup_logger("clinical-devops-assistant")

@asynccontextmanager
async def lifespan(app: FastAPI):
    # Startup sequence
    logger.info("Starting clinical-devops-assistant service...", extra={"port": settings.port})
    await db_manager.initialize()
    await diagnostic_engine.initialize()
    logger.info("clinical-devops-assistant initialized and ready for operator queries")

    yield

    # Graceful shutdown sequence
    logger.info("Graceful shutdown initiated for clinical-devops-assistant...")
    await diagnostic_engine.close()
    await db_manager.close()
    logger.info("clinical-devops-assistant shut down cleanly")

app = FastAPI(
    title="Clinical DevOps Assistant API",
    description="Diagnostic REST API for cluster operators querying telemetry errors, stream drops, and logging incident post-mortems",
    version="1.0.0",
    lifespan=lifespan,
)

# Structured JSON logging middleware
app.add_middleware(StructuredLoggingMiddleware)

# Prometheus metrics instrumentation exposing /metrics
Instrumentator(
    should_group_status_codes=True,
    should_ignore_untemplated=True,
    excluded_handlers=["/metrics", "/livez"]
).instrument(app).expose(app, endpoint="/metrics", tags=["Observability"])

# Include application routes
app.include_router(router)

@app.get("/", tags=["General"])
async def root():
    return {
        "service": "clinical-devops-assistant",
        "description": "Cluster diagnostics & incident post-mortem tracking API",
        "endpoints": [
            "/api/v1/diagnose",
            "/api/v1/post-mortems",
            "/api/v1/cluster/overview",
            "/healthz",
            "/livez",
            "/metrics",
        ],
    }

if __name__ == "__main__":
    uvicorn.run(
        "main:app",
        host="0.0.0.0",
        port=settings.port,
        log_config=None,  # Use custom JSON formatter
        access_log=False,
    )
