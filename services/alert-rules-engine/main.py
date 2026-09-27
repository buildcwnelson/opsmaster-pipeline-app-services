import asyncio
import signal
import sys

from src.config import settings
from src.logger import setup_logger
from src.rules import SlidingWindowEvaluator
from src.sinks import AlertDispatcher
from src.stream_worker import StreamWorker
from src.server import ObservabilityServer

logger = setup_logger("alert-rules-engine")

async def main():
    logger.info("Initializing alert-rules-engine streaming worker...")

    evaluator = SlidingWindowEvaluator(
        window_seconds=settings.sliding_window_seconds,
        min_samples=settings.sliding_window_min_samples
    )

    dispatcher = AlertDispatcher()
    await dispatcher.initialize()

    worker = StreamWorker(evaluator=evaluator, dispatcher=dispatcher)
    await worker.initialize()

    # Pass redis client to dispatcher if available
    dispatcher.redis_client = worker.redis_client

    server = ObservabilityServer(worker=worker, dispatcher=dispatcher)
    await server.start()

    worker_task = asyncio.create_task(worker.run())

    stop_event = asyncio.Event()

    def handle_signal(sig_name):
        logger.info(f"Received signal {sig_name}, starting graceful shutdown...")
        stop_event.set()

    loop = asyncio.get_running_loop()
    for sig in (signal.SIGTERM, signal.SIGINT):
        try:
            loop.add_signal_handler(sig, handle_signal, sig.name)
        except NotImplementedError:
            # Fallback for platforms without add_signal_handler
            signal.signal(sig, lambda s, f: handle_signal(sig.name))

    # Wait until shutdown signal
    await stop_event.wait()

    logger.info(f"Graceful shutdown initiated (timeout: {settings.shutdown_timeout_seconds}s)")

    try:
        # Step 1: Stop consuming from stream and drain in-flight messages
        await asyncio.wait_for(worker.stop(), timeout=5.0)
    except asyncio.TimeoutError:
        logger.warning("Worker stop timed out")

    try:
        # Step 2: Close sinks (Postgres DB pool, webhook HTTP session)
        await asyncio.wait_for(dispatcher.close(), timeout=3.0)
    except asyncio.TimeoutError:
        logger.warning("Dispatcher close timed out")

    try:
        # Step 3: Stop HTTP observability server
        await asyncio.wait_for(server.stop(), timeout=2.0)
    except asyncio.TimeoutError:
        logger.warning("Server stop timed out")

    worker_task.cancel()
    logger.info("alert-rules-engine shut down cleanly")

if __name__ == "__main__":
    try:
        asyncio.run(main())
    except (KeyboardInterrupt, SystemExit):
        pass
