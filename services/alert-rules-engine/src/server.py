import time
from aiohttp import web
from prometheus_client import CONTENT_TYPE_LATEST, generate_latest

from .config import settings
from .logger import setup_logger

logger = setup_logger()

class ObservabilityServer:
    def __init__(self, worker, dispatcher):
        self.worker = worker
        self.dispatcher = dispatcher
        self.start_time = time.time()
        self.app = web.Application()
        self.app.router.add_get("/metrics", self.handle_metrics)
        self.app.router.add_get("/healthz", self.handle_healthz)
        self.app.router.add_get("/livez", self.handle_livez)
        self.app.router.add_get("/", self.handle_root)
        self.runner = None

    async def handle_metrics(self, request: web.Request) -> web.Response:
        resp = generate_latest()
        return web.Response(body=resp, content_type=CONTENT_TYPE_LATEST)

    async def handle_livez(self, request: web.Request) -> web.Response:
        data = {
            "status": "alive",
            "service": "alert-rules-engine",
            "worker_running": self.worker.running,
            "uptime_seconds": round(time.time() - self.start_time, 2)
        }
        return web.json_response(data, status=200)

    async def handle_healthz(self, request: web.Request) -> web.Response:
        checks = {}
        is_ready = True

        # Check worker loop
        if not self.worker.running:
            checks["worker"] = "unhealthy (not running)"
            is_ready = False
        else:
            checks["worker"] = "healthy"

        # Check Redis connection
        if self.worker.redis_client is not None:
            try:
                await self.worker.redis_client.ping()
                checks["redis"] = "healthy"
            except Exception as e:
                checks["redis"] = f"unhealthy: {e}"
                is_ready = False
        else:
            checks["redis"] = "fallback_mock_active"

        # Check Database pool
        if self.dispatcher.db_pool is not None:
            checks["database"] = "connected"
        else:
            checks["database"] = "in_memory_fallback"

        status_code = 200 if is_ready else 503
        data = {
            "status": "ready" if is_ready else "degraded",
            "service": "alert-rules-engine",
            "checks": checks,
            "uptime_seconds": round(time.time() - self.start_time, 2)
        }
        return web.json_response(data, status=status_code)

    async def handle_root(self, request: web.Request) -> web.Response:
        return web.json_response({
            "service": "alert-rules-engine",
            "endpoints": ["/healthz", "/livez", "/metrics"]
        })

    async def start(self):
        self.runner = web.AppRunner(self.app)
        await self.runner.setup()
        site = web.TCPSite(self.runner, "0.0.0.0", settings.http_port)
        await site.start()
        logger.info(f"Observability server listening on port {settings.http_port}")

    async def stop(self):
        logger.info("Stopping observability server...")
        if self.runner:
            await self.runner.cleanup()
