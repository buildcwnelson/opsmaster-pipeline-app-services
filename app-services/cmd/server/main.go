package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/opsmaster/app-services/pkg/alerts"
	"github.com/opsmaster/app-services/pkg/cache"
	"github.com/opsmaster/app-services/pkg/config"
	"github.com/opsmaster/app-services/pkg/database"
	"github.com/opsmaster/app-services/pkg/diagnostics"
	"github.com/opsmaster/app-services/pkg/ingress"
	"github.com/opsmaster/app-services/pkg/logger"
	"github.com/opsmaster/app-services/pkg/metrics"
	"github.com/opsmaster/app-services/pkg/webrtc"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	cfg := config.Load()
	log := logger.Init("app-engine", cfg.LogLevel)

	log.Info("Starting OpsMaster 3-Container Unified App-Engine...",
		slog.String("port", cfg.Port),
		slog.String("redis_addr", cfg.RedisAddr),
	)

	// 1. Initialize PostgreSQL Database
	db, err := database.New(cfg)
	if err != nil {
		log.Error("Database initialization failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Warn("Error closing database connection", slog.String("error", err.Error()))
		}
	}()

	// 2. Initialize Redis Cache & Stream Queue
	c, err := cache.New(cfg)
	if err != nil {
		log.Error("Redis cache initialization failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	defer func() {
		if err := c.Close(); err != nil {
			log.Warn("Error closing cache connection", slog.String("error", err.Error()))
		}
	}()

	// 3. Setup Internal In-Memory Channel for Sub-Millisecond Anomaly Stream Processing
	alertChan := make(chan *ingress.VitalsPayload, 10000)

	// 4. Start In-Memory Alerts Worker Goroutine
	workerCtx, workerCancel := context.WithCancel(context.Background())
	defer workerCancel()

	alertWorker := alerts.NewWorker(cfg, db, c, alertChan)
	alertWorker.Start(workerCtx)

	// 5. Initialize Modular Handlers
	ingressHandler := ingress.NewHandler(cfg, c, alertChan)
	webrtcRouter := webrtc.NewRouter(c)
	diagHandler := diagnostics.NewHandler(db, c)

	mux := http.NewServeMux()

	// Observability & Probes
	startTime := time.Now()
	mux.Handle("/metrics", promhttp.Handler())

	mux.HandleFunc("/livez", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":         "alive",
			"service":        "app-engine",
			"uptime_seconds": time.Since(startTime).Seconds(),
		})
	})

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 1*time.Second)
		defer cancel()

		checks := make(map[string]string)
		isReady := true

		if err := db.Ping(ctx); err != nil {
			checks["postgres"] = "unhealthy: " + err.Error()
			isReady = false
		} else {
			checks["postgres"] = "healthy"
		}

		if err := c.Ping(ctx); err != nil {
			checks["redis"] = "unhealthy: " + err.Error()
			isReady = false
		} else {
			checks["redis"] = "healthy"
		}

		statusCode := http.StatusOK
		statusStr := "ready"
		if !isReady {
			statusCode = http.StatusServiceUnavailable
			statusStr = "degraded"
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":         statusStr,
			"service":        "app-engine",
			"checks":         checks,
			"uptime_seconds": time.Since(startTime).Seconds(),
		})
	})

	// Vitals Ingress (REST & WebSocket)
	mux.HandleFunc("/api/v1/vitals", ingressHandler.IngestREST)
	mux.HandleFunc("/api/v1/vitals/ws", ingressHandler.IngestWebSocket)

	// WebRTC Telehealth Signaling & Sessions
	webrtcRouter.RegisterRoutes(mux)

	// Clinical DevOps Administrative Diagnostics
	diagHandler.RegisterRoutes(mux)

	// Root Information
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"service":"app-engine","architecture":"3-container-consolidated","endpoints":["/api/v1/vitals", "/api/v1/vitals/ws", "/api/v1/telehealth/sessions", "/api/v1/telehealth/ws", "/api/v1/admin/diagnose", "/healthz", "/livez", "/metrics"]}`)
	})

	// Apply Middlewares (Metrics -> Logger -> Handler)
	var handler http.Handler = mux
	handler = logger.Middleware(handler)
	handler = metrics.Middleware(handler)

	server := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		log.Info("Unified HTTP/WS server listening", slog.String("addr", server.Addr))
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()

	// 6. Graceful Shutdown Signal Handling
	shutdownSignal := make(chan os.Signal, 1)
	signal.Notify(shutdownSignal, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)

	select {
	case err := <-serverErrors:
		log.Error("Fatal server runtime error", slog.String("error", err.Error()))
		os.Exit(1)

	case sig := <-shutdownSignal:
		log.Info("Shutdown signal received, initiating graceful teardown",
			slog.String("signal", sig.String()),
			slog.Duration("timeout", cfg.ShutdownTimeout),
		)

		// Stop HTTP server from accepting new traffic
		ctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()

		if err := server.Shutdown(ctx); err != nil {
			log.Error("Server shutdown error, forcing close", slog.String("error", err.Error()))
			_ = server.Close()
		} else {
			log.Info("HTTP/WS listener stopped and client connections drained")
		}

		// Stop alert worker and drain in-flight channel vitals
		alertWorker.Stop()
	}

	log.Info("app-engine stopped cleanly")
}
