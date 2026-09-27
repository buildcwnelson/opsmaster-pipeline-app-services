package alerts

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/opsmaster/app-services/pkg/cache"
	"github.com/opsmaster/app-services/pkg/config"
	"github.com/opsmaster/app-services/pkg/database"
	"github.com/opsmaster/app-services/pkg/ingress"
	"github.com/opsmaster/app-services/pkg/metrics"
)

type Sample struct {
	Timestamp time.Time
	Value     float64
}

type Worker struct {
	cfg        *config.Config
	db         database.DB
	cache      cache.Cache
	inputChan  <-chan *ingress.VitalsPayload
	stopChan   chan struct{}
	wg         sync.WaitGroup
	mu         sync.Mutex
	// Key: patientID:metricType -> list of Samples in window
	windows    map[string][]Sample
}

func NewWorker(cfg *config.Config, db database.DB, c cache.Cache, inputChan <-chan *ingress.VitalsPayload) *Worker {
	return &Worker{
		cfg:       cfg,
		db:        db,
		cache:     c,
		inputChan: inputChan,
		stopChan:  make(chan struct{}),
		windows:   make(map[string][]Sample),
	}
}

func (w *Worker) Start(ctx context.Context) {
	w.wg.Add(1)
	go w.processLoop(ctx)
	slog.Info("Real-time alert rules engine worker goroutine started",
		slog.Duration("window", w.cfg.SlidingWindowSeconds),
		slog.Float64("tachycardia_threshold_bpm", w.cfg.ThresholdTachycardiaBPM),
		slog.Float64("hypoxemia_threshold_spo2", w.cfg.ThresholdHypoxemiaSpO2),
	)
}

func (w *Worker) processLoop(ctx context.Context) {
	defer w.wg.Done()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-w.stopChan:
			slog.Info("Alert rules engine worker draining in-flight vitals...")
			// Drain remaining buffer
			for {
				select {
				case payload := <-w.inputChan:
					w.evaluatePayload(context.Background(), payload)
				default:
					return
				}
			}

		case <-ctx.Done():
			return

		case <-ticker.C:
			w.cleanupStaleWindows()

		case payload, ok := <-w.inputChan:
			if !ok {
				return
			}
			w.evaluatePayload(ctx, payload)
		}
	}
}

func (w *Worker) evaluatePayload(ctx context.Context, p *ingress.VitalsPayload) {
	now := time.Now().UTC()
	key := fmt.Sprintf("%s:%s", p.PatientID, p.MetricType)

	var anomaly *database.TelemetryAlert

	// 1. Scalar metrics: Evaluate against sliding window
	if p.Value != nil {
		w.mu.Lock()
		samples := append(w.windows[key], Sample{Timestamp: now, Value: *p.Value})

		// Evict samples older than sliding window
		cutoff := now.Add(-w.cfg.SlidingWindowSeconds)
		validIdx := 0
		for i, s := range samples {
			if s.Timestamp.After(cutoff) {
				validIdx = i
				break
			}
		}
		samples = samples[validIdx:]
		w.windows[key] = samples

		// Update active patients gauge
		patients := make(map[string]bool)
		for k := range w.windows {
			patients[k] = true
		}
		metrics.ActivePatientsGauge.Set(float64(len(patients)))

		currVal := *p.Value
		sampleCount := len(samples)

		if sampleCount >= w.cfg.SlidingWindowMinSamples {
			sum := 0.0
			for _, s := range samples {
				sum += s.Value
			}
			avgVal := sum / float64(sampleCount)

			switch p.MetricType {
			case ingress.MetricHeartRate:
				if avgVal >= w.cfg.ThresholdTachycardiaBPM && currVal >= w.cfg.ThresholdTachycardiaBPM {
					anomaly = &database.TelemetryAlert{
						ID:             generateUUID(),
						PatientID:      p.PatientID,
						DeviceID:       p.DeviceID,
						AnomalyType:    "TACHYCARDIA",
						Severity:       "CRITICAL",
						CurrentValue:   currVal,
						ThresholdValue: w.cfg.ThresholdTachycardiaBPM,
						Unit:           "bpm",
						Description:    fmt.Sprintf("Sustained tachycardia: current %.1f BPM (window avg %.1f BPM) exceeds %.0f BPM threshold across %d samples in %v", currVal, avgVal, w.cfg.ThresholdTachycardiaBPM, sampleCount, w.cfg.SlidingWindowSeconds),
						DetectedAt:     now,
					}
				} else if avgVal <= w.cfg.ThresholdBradycardiaBPM && currVal <= w.cfg.ThresholdBradycardiaBPM {
					anomaly = &database.TelemetryAlert{
						ID:             generateUUID(),
						PatientID:      p.PatientID,
						DeviceID:       p.DeviceID,
						AnomalyType:    "BRADYCARDIA",
						Severity:       "CRITICAL",
						CurrentValue:   currVal,
						ThresholdValue: w.cfg.ThresholdBradycardiaBPM,
						Unit:           "bpm",
						Description:    fmt.Sprintf("Severe bradycardia: current %.1f BPM (window avg %.1f BPM) below %.0f BPM threshold", currVal, avgVal, w.cfg.ThresholdBradycardiaBPM),
						DetectedAt:     now,
					}
				}

			case ingress.MetricSpO2:
				if avgVal < w.cfg.ThresholdCriticalSpO2 {
					anomaly = &database.TelemetryAlert{
						ID:             generateUUID(),
						PatientID:      p.PatientID,
						DeviceID:       p.DeviceID,
						AnomalyType:    "CRITICAL_HYPOXEMIA",
						Severity:       "CRITICAL",
						CurrentValue:   currVal,
						ThresholdValue: w.cfg.ThresholdCriticalSpO2,
						Unit:           "%",
						Description:    fmt.Sprintf("Critical arterial hypoxemia: current %.1f%% (window avg %.1f%%) below critical limit %.0f%%", currVal, avgVal, w.cfg.ThresholdCriticalSpO2),
						DetectedAt:     now,
					}
				} else if avgVal < w.cfg.ThresholdHypoxemiaSpO2 {
					anomaly = &database.TelemetryAlert{
						ID:             generateUUID(),
						PatientID:      p.PatientID,
						DeviceID:       p.DeviceID,
						AnomalyType:    "HYPOXEMIA",
						Severity:       "WARNING",
						CurrentValue:   currVal,
						ThresholdValue: w.cfg.ThresholdHypoxemiaSpO2,
						Unit:           "%",
						Description:    fmt.Sprintf("Hypoxemia warning: current %.1f%% (window avg %.1f%%) below threshold %.0f%%", currVal, avgVal, w.cfg.ThresholdHypoxemiaSpO2),
						DetectedAt:     now,
					}
				}

			case ingress.MetricBloodPressureSystolic:
				if avgVal >= w.cfg.ThresholdSystolicBPHigh {
					anomaly = &database.TelemetryAlert{
						ID:             generateUUID(),
						PatientID:      p.PatientID,
						DeviceID:       p.DeviceID,
						AnomalyType:    "HYPERTENSIVE_CRISIS",
						Severity:       "CRITICAL",
						CurrentValue:   currVal,
						ThresholdValue: w.cfg.ThresholdSystolicBPHigh,
						Unit:           "mmHg",
						Description:    fmt.Sprintf("Hypertensive crisis: systolic BP %.1f mmHg exceeds threshold %.0f mmHg", currVal, w.cfg.ThresholdSystolicBPHigh),
						DetectedAt:     now,
					}
				}
			}
		}
		w.mu.Unlock()
	}

	// 2. Waveform metrics: ECG Arrhythmia detection
	if p.MetricType == ingress.MetricECGWaveform && len(p.Waveform) > 0 {
		minV := math.MaxFloat64
		maxV := -math.MaxFloat64
		for _, v := range p.Waveform {
			if v < minV {
				minV = v
			}
			if v > maxV {
				maxV = v
			}
		}
		peakToPeak := maxV - minV
		if peakToPeak > 4.0 {
			anomaly = &database.TelemetryAlert{
				ID:             generateUUID(),
				PatientID:      p.PatientID,
				DeviceID:       p.DeviceID,
				AnomalyType:    "ECG_ARRHYTHMIA",
				Severity:       "CRITICAL",
				CurrentValue:   peakToPeak,
				ThresholdValue: 4.0,
				Unit:           "mV",
				Description:    fmt.Sprintf("ECG Arrhythmia / Ventricular Fibrillation signature: peak-to-peak amplitude %.2f mV exceeds 4.0 mV threshold", peakToPeak),
				DetectedAt:     now,
			}
		}
	}

	// 3. Dispatch alert if triggered
	if anomaly != nil {
		w.dispatchAlert(ctx, anomaly)
	}
}

func (w *Worker) dispatchAlert(ctx context.Context, alert *database.TelemetryAlert) {
	metrics.AnomaliesDetectedTotal.WithLabelValues(alert.AnomalyType, alert.Severity).Inc()

	slog.Warn("CRITICAL MEDICAL ANOMALY DETECTED",
		slog.String("alert_id", alert.ID),
		slog.String("patient_id", alert.PatientID),
		slog.String("anomaly_type", alert.AnomalyType),
		slog.String("severity", alert.Severity),
		slog.Float64("current_value", alert.CurrentValue),
		slog.Float64("threshold", alert.ThresholdValue),
		slog.String("description", alert.Description),
	)

	// Persist to PostgreSQL database
	if err := w.db.SaveAlert(ctx, alert); err != nil {
		slog.Error("Failed to persist alert to PostgreSQL", slog.String("error", err.Error()))
	}

	// Broadcast to Redis alerts channel
	if err := w.cache.PublishEvent(ctx, w.cfg.RedisAlertsChannel, alert); err != nil {
		slog.Warn("Failed to broadcast alert to Redis channel", slog.String("error", err.Error()))
	}
}

func (w *Worker) cleanupStaleWindows() {
	w.mu.Lock()
	defer w.mu.Unlock()

	cutoff := time.Now().UTC().Add(-w.cfg.SlidingWindowSeconds * 2)
	for k, samples := range w.windows {
		if len(samples) == 0 || samples[len(samples)-1].Timestamp.Before(cutoff) {
			delete(w.windows, k)
		}
	}
}

func (w *Worker) Stop() {
	close(w.stopChan)
	w.wg.Wait()
	slog.Info("Alert rules engine worker stopped cleanly")
}

func generateUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]),
		hex.EncodeToString(b[4:6]),
		hex.EncodeToString(b[6:8]),
		hex.EncodeToString(b[8:10]),
		hex.EncodeToString(b[10:16]))
}
