import asyncio
import json
import uuid
from datetime import datetime, timezone
from typing import Optional
import aiohttp

from .config import settings
from .logger import setup_logger
from .metrics import ALERT_DISPATCH_TOTAL
from .models import MedicalAnomaly, AlertNotification

logger = setup_logger()

class AlertDispatcher:
    def __init__(self, redis_client=None):
        self.redis_client = redis_client
        self.http_session: Optional[aiohttp.ClientSession] = None
        self.db_pool = None
        self.in_memory_alerts = []

    async def initialize(self):
        timeout = aiohttp.ClientTimeout(total=5.0)
        self.http_session = aiohttp.ClientSession(timeout=timeout)

        # Attempt PostgreSQL connection
        if "postgresql" in settings.database_url:
            try:
                import asyncpg
                self.db_pool = await asyncpg.create_pool(
                    settings.database_url,
                    min_size=1,
                    max_size=10,
                    timeout=3.0,
                    command_timeout=5.0
                )
                async with self.db_pool.acquire() as conn:
                    await conn.execute("""
                        CREATE TABLE IF NOT EXISTS medical_alerts (
                            id UUID PRIMARY KEY,
                            patient_id VARCHAR(128) NOT NULL,
                            device_id VARCHAR(128) NOT NULL,
                            anomaly_type VARCHAR(64) NOT NULL,
                            severity VARCHAR(32) NOT NULL,
                            current_value DOUBLE PRECISION NOT NULL,
                            threshold_value DOUBLE PRECISION NOT NULL,
                            description TEXT NOT NULL,
                            detected_at TIMESTAMPTZ NOT NULL,
                            created_at TIMESTAMPTZ DEFAULT NOW()
                        );
                        CREATE INDEX IF NOT EXISTS idx_medical_alerts_patient ON medical_alerts(patient_id);
                    """)
                logger.info("PostgreSQL alert sink initialized successfully")
            except Exception as e:
                logger.warning(
                    f"Could not connect to PostgreSQL ({e}). Using in-memory alert sink fallback."
                )
                self.db_pool = None

    async def dispatch(self, anomaly: MedicalAnomaly) -> AlertNotification:
        alert_id = str(uuid.uuid4())
        notification = AlertNotification(
            alert_id=alert_id,
            anomaly=anomaly,
            dispatched_at=datetime.now(timezone.utc)
        )

        logger.critical(
            f"URGENT MEDICAL ALERT [{anomaly.severity}]: {anomaly.anomaly_type} for patient {anomaly.patient_id}",
            extra={
                "alert_id": alert_id,
                "patient_id": anomaly.patient_id,
                "anomaly_type": anomaly.anomaly_type,
                "severity": anomaly.severity,
                "current_val": anomaly.current_value,
                "threshold": anomaly.threshold_value,
            }
        )

        # 1. Database sink
        asyncio.create_task(self._save_to_database(notification))

        # 2. External Webhook sink
        asyncio.create_task(self._send_webhook(notification))

        # 3. Redis Pub/Sub broadcast sink
        if self.redis_client:
            asyncio.create_task(self._publish_redis(notification))

        return notification

    async def _save_to_database(self, alert: AlertNotification):
        a = alert.anomaly
        if self.db_pool:
            try:
                async with self.db_pool.acquire() as conn:
                    await conn.execute(
                        """
                        INSERT INTO medical_alerts 
                        (id, patient_id, device_id, anomaly_type, severity, current_value, threshold_value, description, detected_at)
                        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
                        """,
                        uuid.UUID(alert.alert_id),
                        a.patient_id,
                        a.device_id,
                        a.anomaly_type,
                        a.severity,
                        float(a.current_value),
                        float(a.threshold_value),
                        a.description,
                        a.detected_at
                    )
                ALERT_DISPATCH_TOTAL.labels(sink_type="database", status="success").inc()
            except Exception as e:
                logger.error(f"Failed to persist alert to PostgreSQL: {e}")
                ALERT_DISPATCH_TOTAL.labels(sink_type="database", status="error").inc()
                self.in_memory_alerts.append(alert.model_dump(mode="json"))
        else:
            self.in_memory_alerts.append(alert.model_dump(mode="json"))
            if len(self.in_memory_alerts) > 500:
                self.in_memory_alerts.pop(0)
            ALERT_DISPATCH_TOTAL.labels(sink_type="database", status="in_memory_fallback").inc()

    async def _send_webhook(self, alert: AlertNotification):
        if not self.http_session or not settings.webhook_url:
            return

        payload = alert.model_dump(mode="json")
        for attempt in range(1, 3):
            try:
                async with self.http_session.post(
                    settings.webhook_url,
                    json=payload,
                    headers={"Content-Type": "application/json"}
                ) as resp:
                    if resp.status < 400:
                        ALERT_DISPATCH_TOTAL.labels(sink_type="webhook", status="success").inc()
                        return
                    else:
                        logger.warning(f"Webhook responded with HTTP {resp.status} on attempt {attempt}")
            except Exception as e:
                logger.warning(f"Webhook dispatch failed on attempt {attempt}: {e}")
                await asyncio.sleep(0.5)

        ALERT_DISPATCH_TOTAL.labels(sink_type="webhook", status="failed").inc()

    async def _publish_redis(self, alert: AlertNotification):
        try:
            payload = json.dumps(alert.model_dump(mode="json"))
            await self.redis_client.publish("alerts:critical", payload)
            ALERT_DISPATCH_TOTAL.labels(sink_type="redis_pubsub", status="success").inc()
        except Exception as e:
            logger.warning(f"Failed to publish alert to Redis pubsub: {e}")
            ALERT_DISPATCH_TOTAL.labels(sink_type="redis_pubsub", status="error").inc()

    async def close(self):
        logger.info("Closing alert sink connections...")
        if self.http_session and not self.http_session.closed:
            await self.http_session.close()
        if self.db_pool:
            await self.db_pool.close()
