import uuid
from datetime import datetime, timezone
from typing import List, Optional
from sqlalchemy import Column, String, DateTime, JSON, Text
from sqlalchemy.orm import declarative_base
from sqlalchemy.ext.asyncio import create_async_engine, async_sessionmaker, AsyncSession
from sqlalchemy.future import select

from .config import settings
from .logger import setup_logger
from .metrics import DATABASE_HEALTH_STATUS

logger = setup_logger()

Base = declarative_base()

class PostMortemRecord(Base):
    __tablename__ = "post_mortem_reports"

    id = Column(String(36), primary_key=True, default=lambda: str(uuid.uuid4()))
    incident_title = Column(String(255), nullable=False)
    incident_severity = Column(String(32), nullable=False)
    summary = Column(Text, nullable=False)
    root_cause = Column(Text, nullable=False)
    affected_services = Column(JSON, nullable=False)
    impact_description = Column(Text, nullable=False)
    start_time = Column(DateTime(timezone=True), nullable=False)
    resolved_at = Column(DateTime(timezone=True), nullable=True)
    action_items = Column(JSON, default=list)
    author = Column(String(128), default="Cluster Operator")
    created_at = Column(DateTime(timezone=True), default=lambda: datetime.now(timezone.utc))
    updated_at = Column(DateTime(timezone=True), default=lambda: datetime.now(timezone.utc))

class DatabaseManager:
    def __init__(self):
        self.engine = None
        self.session_factory = None
        self.is_connected = False
        self.in_memory_records = []

    async def initialize(self):
        db_url = settings.database_url

        try:
            logger.info(f"Connecting to database: {db_url.split('@')[-1] if '@' in db_url else db_url}")
            self.engine = create_async_engine(
                db_url,
                echo=False,
                pool_pre_ping=True,
                pool_size=5,
                max_overflow=10
            )
            self.session_factory = async_sessionmaker(
                bind=self.engine,
                expire_on_commit=False,
                class_=AsyncSession
            )

            async with self.engine.begin() as conn:
                await conn.run_sync(Base.metadata.create_all)

            self.is_connected = True
            DATABASE_HEALTH_STATUS.set(1)
            logger.info("Database schema verified and connection established")
        except Exception as e:
            DATABASE_HEALTH_STATUS.set(0)
            logger.warning(f"Could not connect to configured DB ({e}). Initializing SQLite/In-memory fallback.")
            if settings.enable_sqlite_fallback:
                try:
                    fallback_url = "sqlite+aiosqlite:///./clinical_fallback.db"
                    self.engine = create_async_engine(fallback_url, echo=False)
                    self.session_factory = async_sessionmaker(
                        bind=self.engine,
                        expire_on_commit=False,
                        class_=AsyncSession
                    )
                    async with self.engine.begin() as conn:
                        await conn.run_sync(Base.metadata.create_all)
                    self.is_connected = True
                    DATABASE_HEALTH_STATUS.set(1)
                    logger.info("SQLite fallback database initialized")
                except Exception as ex:
                    logger.error(f"Failed to initialize SQLite fallback: {ex}")
                    self.is_connected = False

    async def save_post_mortem(self, pm_data: dict) -> PostMortemRecord:
        record_id = pm_data.get("id") or str(uuid.uuid4())
        record = PostMortemRecord(
            id=record_id,
            incident_title=pm_data["incident_title"],
            incident_severity=pm_data["incident_severity"],
            summary=pm_data["summary"],
            root_cause=pm_data["root_cause"],
            affected_services=pm_data["affected_services"],
            impact_description=pm_data["impact_description"],
            start_time=pm_data["start_time"],
            resolved_at=pm_data.get("resolved_at"),
            action_items=pm_data.get("action_items", []),
            author=pm_data.get("author", "Cluster Operator"),
            created_at=datetime.now(timezone.utc),
            updated_at=datetime.now(timezone.utc),
        )

        if self.session_factory:
            async with self.session_factory() as session:
                async with session.begin():
                    session.add(record)
            return record
        else:
            self.in_memory_records.append(record)
            return record

    async def list_post_mortems(self, severity: Optional[str] = None) -> List[PostMortemRecord]:
        if self.session_factory:
            async with self.session_factory() as session:
                query = select(PostMortemRecord).order_by(PostMortemRecord.created_at.desc())
                if severity:
                    query = query.where(PostMortemRecord.incident_severity == severity)
                result = await session.execute(query)
                return list(result.scalars().all())
        else:
            records = self.in_memory_records
            if severity:
                records = [r for r in records if r.incident_severity == severity]
            return records

    async def check_health(self) -> bool:
        if not self.engine:
            return False
        try:
            async with self.engine.connect() as conn:
                await conn.exec_driver_sql("SELECT 1")
            DATABASE_HEALTH_STATUS.set(1)
            return True
        except Exception:
            DATABASE_HEALTH_STATUS.set(0)
            return False

    async def close(self):
        if self.engine:
            await self.engine.dispose()
            logger.info("Database engine disposed")

db_manager = DatabaseManager()
