import time
from collections import deque
from datetime import datetime, timezone
from typing import Dict, List, Optional, Tuple

from .config import settings
from .logger import setup_logger
from .metrics import RULE_EVAL_DURATION_SECONDS, ACTIVE_PATIENTS_IN_WINDOW
from .models import TelemetryRecord, MedicalAnomaly

logger = setup_logger()

class SlidingWindowSample:
    __slots__ = ("timestamp", "value")
    def __init__(self, timestamp: float, value: float):
        self.timestamp = timestamp
        self.value = value

class SlidingWindowEvaluator:
    def __init__(self, window_seconds: int = 30, min_samples: int = 2):
        self.window_seconds = window_seconds
        self.min_samples = min_samples
        # key: (patient_id, metric_type) -> deque of SlidingWindowSample
        self.windows: Dict[Tuple[str, str], deque[SlidingWindowSample]] = {}

    def _evict_stale(self, key: Tuple[str, str], now: float):
        q = self.windows.get(key)
        if not q:
            return
        cutoff = now - self.window_seconds
        while q and q[0].timestamp < cutoff:
            q.popleft()
        if not q:
            del self.windows[key]

    @RULE_EVAL_DURATION_SECONDS.time()
    def evaluate(self, record: TelemetryRecord) -> Optional[MedicalAnomaly]:
        now = time.time()
        key = (record.patient_id, record.metric_type)

        if record.value is None and not record.waveform:
            return None

        # Handle scalar metrics
        if record.value is not None:
            if key not in self.windows:
                self.windows[key] = deque()
            
            q = self.windows[key]
            q.append(SlidingWindowSample(now, record.value))
            self._evict_stale(key, now)

            # Update gauge
            unique_patients = {k[0] for k in self.windows.keys()}
            ACTIVE_PATIENTS_IN_WINDOW.set(len(unique_patients))

            if len(q) < self.min_samples:
                # Not enough points in the window yet to confirm trend
                return None

            values = [s.value for s in q]
            avg_val = sum(values) / len(values)
            curr_val = record.value

            # Rule 1: Tachycardia
            if record.metric_type == "heart_rate":
                if avg_val >= settings.tachycardia_bpm and curr_val >= settings.tachycardia_bpm:
                    return MedicalAnomaly(
                        patient_id=record.patient_id,
                        device_id=record.device_id,
                        anomaly_type="TACHYCARDIA",
                        severity="CRITICAL",
                        current_value=curr_val,
                        threshold_value=settings.tachycardia_bpm,
                        unit="bpm",
                        window_samples=len(values),
                        window_avg=round(avg_val, 2),
                        description=f"Sustained tachycardia detected: avg {round(avg_val, 1)} BPM (current {curr_val} BPM) exceeds threshold {settings.tachycardia_bpm} BPM across {len(values)} samples in {self.window_seconds}s window",
                        detected_at=datetime.now(timezone.utc)
                    )
                elif avg_val <= settings.bradycardia_bpm and curr_val <= settings.bradycardia_bpm:
                    return MedicalAnomaly(
                        patient_id=record.patient_id,
                        device_id=record.device_id,
                        anomaly_type="BRADYCARDIA",
                        severity="CRITICAL",
                        current_value=curr_val,
                        threshold_value=settings.bradycardia_bpm,
                        unit="bpm",
                        window_samples=len(values),
                        window_avg=round(avg_val, 2),
                        description=f"Severe bradycardia detected: avg {round(avg_val, 1)} BPM (current {curr_val} BPM) below threshold {settings.bradycardia_bpm} BPM",
                        detected_at=datetime.now(timezone.utc)
                    )

            # Rule 2: Oxygen Saturation (SpO2)
            elif record.metric_type == "spo2":
                if avg_val < settings.critical_hypoxemia_spo2:
                    return MedicalAnomaly(
                        patient_id=record.patient_id,
                        device_id=record.device_id,
                        anomaly_type="CRITICAL_HYPOXEMIA",
                        severity="CRITICAL",
                        current_value=curr_val,
                        threshold_value=settings.critical_hypoxemia_spo2,
                        unit="%",
                        window_samples=len(values),
                        window_avg=round(avg_val, 2),
                        description=f"Critical arterial oxygen desaturation: avg {round(avg_val, 1)}% SpO2 below critical limit {settings.critical_hypoxemia_spo2}%",
                        detected_at=datetime.now(timezone.utc)
                    )
                elif avg_val < settings.hypoxemia_spo2:
                    return MedicalAnomaly(
                        patient_id=record.patient_id,
                        device_id=record.device_id,
                        anomaly_type="HYPOXEMIA",
                        severity="WARNING",
                        current_value=curr_val,
                        threshold_value=settings.hypoxemia_spo2,
                        unit="%",
                        window_samples=len(values),
                        window_avg=round(avg_val, 2),
                        description=f"Hypoxemia warning: avg {round(avg_val, 1)}% SpO2 below warning threshold {settings.hypoxemia_spo2}%",
                        detected_at=datetime.now(timezone.utc)
                    )

            # Rule 3: Blood Pressure Systolic
            elif record.metric_type == "blood_pressure_systolic":
                if avg_val >= settings.systolic_high:
                    return MedicalAnomaly(
                        patient_id=record.patient_id,
                        device_id=record.device_id,
                        anomaly_type="HYPERTENSIVE_CRISIS",
                        severity="CRITICAL",
                        current_value=curr_val,
                        threshold_value=settings.systolic_high,
                        unit="mmHg",
                        window_samples=len(values),
                        window_avg=round(avg_val, 2),
                        description=f"Hypertensive crisis: systolic BP {curr_val} mmHg (avg {round(avg_val, 1)} mmHg) exceeds {settings.systolic_high} mmHg",
                        detected_at=datetime.now(timezone.utc)
                    )

        # Rule 4: ECG Waveform Arrhythmia detection
        elif record.waveform and record.metric_type == "ecg_waveform":
            max_peak = max(record.waveform)
            min_peak = min(record.waveform)
            peak_to_peak = max_peak - min_peak
            if peak_to_peak > 4.0:  # Excessive mV amplitude spike / fibrillatory wave
                return MedicalAnomaly(
                    patient_id=record.patient_id,
                    device_id=record.device_id,
                    anomaly_type="ECG_ARRHYTHMIA",
                    severity="CRITICAL",
                    current_value=round(peak_to_peak, 2),
                    threshold_value=4.0,
                    unit="mV",
                    window_samples=len(record.waveform),
                    window_avg=round(peak_to_peak, 2),
                    description=f"ECG Arrhythmia / Ventricular Fibrillation waveform signature detected (Peak-to-Peak: {round(peak_to_peak, 2)} mV)",
                    detected_at=datetime.now(timezone.utc)
                )

        return None
