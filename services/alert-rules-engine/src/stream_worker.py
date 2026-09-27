import asyncio
import json
import time
from datetime import datetime, timezone
import redis.asyncio as aioredis

from .config import settings
from .logger import setup_logger
from .metrics import TELEMETRY_CONSUMED_TOTAL, ANOMALIES_DETECTED_TOTAL, WORKER_HEARTBEAT
from .models import TelemetryRecord
from .rules import SlidingWindowEvaluator
from .sinks import AlertDispatcher

logger = setup_logger()

class StreamWorker:
    def __init__(self, evaluator: SlidingWindowEvaluator, dispatcher: AlertDispatcher):
        self.evaluator = evaluator
        self.dispatcher = dispatcher
        self.redis_client = None
        self.running = False
        self.stop_event = asyncio.Event()
        self.in_flight_count = 0

    async def initialize(self):
        try:
            self.redis_client = aioredis.from_url(
                f"redis://{settings.redis_addr}",
                password=settings.redis_password or None,
                decode_responses=True,
                socket_timeout=3.0
            )
            await self.redis_client.ping()
            logger.info(f"Worker connected to Redis stream at {settings.redis_addr}")

            # Create consumer group if not existing
            try:
                await self.redis_client.xgroup_create(
                    name=settings.redis_stream,
                    groupname=settings.redis_consumer_group,
                    id="$",
                    mkstream=True
                )
                logger.info(f"Created consumer group {settings.redis_consumer_group}")
            except Exception as e:
                if "BUSYGROUP" in str(e):
                    logger.debug("Consumer group already exists")
                else:
                    logger.warning(f"Consumer group init note: {e}")

        except Exception as e:
            logger.warning(
                f"Redis connection failed: {e}. Running in Mock Stream mode."
            )
            self.redis_client = None

    async def run(self):
        self.running = True
        logger.info(
            f"Streaming worker started. Consumer: {settings.redis_consumer_name}, Group: {settings.redis_consumer_group}"
        )

        while not self.stop_event.is_set():
            try:
                WORKER_HEARTBEAT.set(time.time())

                if self.redis_client:
                    # Read from Redis stream
                    entries = await self.redis_client.xreadgroup(
                        groupname=settings.redis_consumer_group,
                        consumername=settings.redis_consumer_name,
                        streams={settings.redis_stream: ">"},
                        count=100,
                        block=1000
                    )

                    if entries:
                        for stream_name, messages in entries:
                            for msg_id, fields in messages:
                                await self._process_message(msg_id, fields)
                else:
                    # Mock sleep when Redis is unavailable
                    await asyncio.sleep(0.5)

            except asyncio.CancelledError:
                break
            except Exception as e:
                logger.error(f"Error in stream processing loop: {e}", exc_info=True)
                await asyncio.sleep(1.0)

        self.running = False
        logger.info("Stream worker loop exited")

    async def _process_message(self, msg_id: str, fields: dict):
        self.in_flight_count += 1
        try:
            raw_data = fields.get("data")
            if not raw_data:
                # May have individual fields
                raw_data = json.dumps(fields)

            payload_dict = json.loads(raw_data)
            record = TelemetryRecord(**payload_dict)

            TELEMETRY_CONSUMED_TOTAL.labels(metric_type=record.metric_type, status="success").inc()

            # Sliding window evaluation
            anomaly = self.evaluator.evaluate(record)
            if anomaly:
                ANOMALIES_DETECTED_TOTAL.labels(
                    anomaly_type=anomaly.anomaly_type,
                    severity=anomaly.severity
                ).inc()
                await self.dispatcher.dispatch(anomaly)

            # Acknowledge message in Redis stream
            if self.redis_client:
                await self.redis_client.xack(
                    settings.redis_stream,
                    settings.redis_consumer_group,
                    msg_id
                )

        except Exception as e:
            logger.error(f"Failed to process message {msg_id}: {e}")
            TELEMETRY_CONSUMED_TOTAL.labels(metric_type="unknown", status="error").inc()
        finally:
            self.in_flight_count -= 1

    async def stop(self):
        logger.info("Signaling stream worker to stop...")
        self.stop_event.set()
        # Wait briefly for in-flight messages to drain
        for _ in range(50):
            if self.in_flight_count <= 0:
                break
            await asyncio.sleep(0.1)

        if self.redis_client:
            await self.redis_client.aclose()
        logger.info("Stream worker stopped cleanly")
