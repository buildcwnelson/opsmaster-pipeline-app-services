import asyncio
import time
import uuid
from typing import Dict, Any, List
import aiohttp

from .config import settings
from .logger import setup_logger
from .metrics import DIAGNOSIS_LATENCY_SECONDS
from .models import DiagnosticReport, DiagnosticFinding

logger = setup_logger()

class ClusterDiagnosticEngine:
    def __init__(self):
        self.session: aiohttp.ClientSession = None

    async def initialize(self):
        timeout = aiohttp.ClientTimeout(total=4.0)
        self.session = aiohttp.ClientSession(timeout=timeout)

    async def run_diagnostics(self, window_minutes: int = 15) -> DiagnosticReport:
        start_time = time.time()
        findings: List[DiagnosticFinding] = []
        metrics_summary: Dict[str, Any] = {}

        # Query metrics & health from vitals-ingress-api, alert-rules-engine, session-router
        tasks = [
            self._probe_service("vitals-ingress-api", settings.vitals_ingress_url),
            self._probe_service("alert-rules-engine", settings.alert_engine_url),
            self._probe_service("session-router", settings.session_router_url),
        ]

        service_results = await asyncio.gather(*tasks, return_exceptions=True)

        for res in service_results:
            if isinstance(res, Exception):
                continue
            service_name = res["service"]
            metrics_summary[service_name] = res

            # Evaluate diagnostic rules on probed data
            if not res.get("healthy"):
                findings.append(DiagnosticFinding(
                    finding_id=f"find-{uuid.uuid4().hex[:8]}",
                    component=service_name,
                    severity="CRITICAL",
                    category="SERVICE_UNREACHABLE",
                    summary=f"Service {service_name} is unreachable or failing health checks",
                    recommended_action="Inspect container logs and restart failed pod/service instance.",
                    evidence={"error": res.get("error"), "endpoint": res.get("url")}
                ))
            else:
                parsed_metrics = res.get("metrics", {})

                # Check for vitals validation drops
                if service_name == "vitals-ingress-api":
                    dropped_points = parsed_metrics.get("validation_errors_count", 0)
                    if dropped_points > 50:
                        findings.append(DiagnosticFinding(
                            finding_id=f"find-{uuid.uuid4().hex[:8]}",
                            component=service_name,
                            severity="WARNING",
                            category="INGESTION_ERROR",
                            summary=f"Elevated telemetry validation drop rate: {dropped_points} malformed payloads rejected",
                            recommended_action="Verify wearable device firmware payload schema compatibility.",
                            evidence={"dropped_count": dropped_points}
                        ))

                # Check for streaming lag / anomaly bursts
                elif service_name == "alert-rules-engine":
                    stream_lag = parsed_metrics.get("stream_lag", 0)
                    if stream_lag > 500:
                        findings.append(DiagnosticFinding(
                            finding_id=f"find-{uuid.uuid4().hex[:8]}",
                            component=service_name,
                            severity="CRITICAL",
                            category="STREAM_DROP",
                            summary=f"Redis consumer stream lag spike: {stream_lag} unread telemetry events",
                            recommended_action="Scale out alert-rules-engine worker replicas in consumer group.",
                            evidence={"stream_lag": stream_lag}
                        ))

        # Overall health determination
        critical_count = sum(1 for f in findings if f.severity == "CRITICAL")
        warning_count = sum(1 for f in findings if f.severity == "WARNING")

        if critical_count > 0:
            overall_health = "CRITICAL"
        elif warning_count > 0:
            overall_health = "DEGRADED"
        else:
            overall_health = "HEALTHY"

        DIAGNOSIS_LATENCY_SECONDS.observe(time.time() - start_time)

        return DiagnosticReport(
            report_id=f"diag-{uuid.uuid4().hex[:10]}",
            overall_health=overall_health,
            analyzed_window_minutes=window_minutes,
            findings=findings,
            summary_metrics=metrics_summary,
        )

    async def _probe_service(self, service_name: str, base_url: str) -> Dict[str, Any]:
        result = {
            "service": service_name,
            "url": base_url,
            "healthy": False,
            "latency_ms": None,
            "error": None,
            "metrics": {},
        }

        if not self.session:
            result["error"] = "HTTP client session not ready"
            return result

        start = time.time()
        try:
            async with self.session.get(f"{base_url}/healthz") as resp:
                result["latency_ms"] = round((time.time() - start) * 1000, 2)
                result["healthy"] = (resp.status == 200)
                if resp.status != 200:
                    result["error"] = f"HTTP {resp.status}"

            # Try to fetch metrics
            try:
                async with self.session.get(f"{base_url}/metrics") as m_resp:
                    if m_resp.status == 200:
                        text = await m_resp.text()
                        result["metrics"] = self._parse_prometheus_sample(text)
            except Exception:
                pass

        except Exception as e:
            result["latency_ms"] = round((time.time() - start) * 1000, 2)
            result["error"] = str(e)
            result["healthy"] = False

        return result

    def _parse_prometheus_sample(self, raw_metrics: str) -> Dict[str, Any]:
        data = {}
        for line in raw_metrics.splitlines():
            line = line.strip()
            if not line or line.startswith("#"):
                continue
            if "validation_error" in line:
                try:
                    val = float(line.split()[-1])
                    data["validation_errors_count"] = val
                except Exception:
                    pass
            elif "stream_lag" in line:
                try:
                    val = float(line.split()[-1])
                    data["stream_lag"] = val
                except Exception:
                    pass
        return data

    async def close(self):
        if self.session and not self.session.closed:
            await self.session.close()

diagnostic_engine = ClusterDiagnosticEngine()
