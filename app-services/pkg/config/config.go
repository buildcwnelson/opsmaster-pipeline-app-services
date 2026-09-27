package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Port                     string
	LogLevel                 string
	ShutdownTimeout          time.Duration
	EnableMockFallback       bool

	// PostgreSQL
	DatabaseURL              string

	// Redis
	RedisAddr                string
	RedisPassword            string
	RedisDB                  int
	RedisVitalsStream        string
	RedisAlertsChannel       string
	RedisSessionPrefix       string
	RedisSessionTTL          time.Duration

	// Alert Engine Thresholds
	SlidingWindowSeconds     time.Duration
	SlidingWindowMinSamples  int
	ThresholdTachycardiaBPM  float64
	ThresholdBradycardiaBPM  float64
	ThresholdHypoxemiaSpO2   float64
	ThresholdCriticalSpO2    float64
	ThresholdSystolicBPHigh  float64
}

func Load() *Config {
	dbURL := getEnv("DATABASE_URL", "")
	if dbURL == "" {
		host := getEnv("POSTGRES_HOST", "localhost")
		port := getEnv("POSTGRES_PORT", "5432")
		user := getEnv("POSTGRES_USER", "postgres")
		pass := getEnv("POSTGRES_PASSWORD", "postgrespassword")
		db := getEnv("POSTGRES_DB", "clinical_db")
		dbURL = fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable", user, pass, host, port, db)
	}

	return &Config{
		Port:                    getEnv("PORT", "8080"),
		LogLevel:                getEnv("LOG_LEVEL", "info"),
		ShutdownTimeout:         time.Duration(getEnvAsInt("SHUTDOWN_TIMEOUT_SECONDS", 10)) * time.Second,
		EnableMockFallback:      getEnvAsBool("ENABLE_MOCK_FALLBACK", true),

		DatabaseURL:             dbURL,

		RedisAddr:               getEnv("REDIS_ADDR", "localhost:6379"),
		RedisPassword:           getEnv("REDIS_PASSWORD", ""),
		RedisDB:                 getEnvAsInt("REDIS_DB", 0),
		RedisVitalsStream:       getEnv("REDIS_VITALS_STREAM", "vitals:stream"),
		RedisAlertsChannel:      getEnv("REDIS_ALERTS_CHANNEL", "vitals:alerts:critical"),
		RedisSessionPrefix:      getEnv("REDIS_SESSION_PREFIX", "opsmaster:session:"),
		RedisSessionTTL:         time.Duration(getEnvAsInt("REDIS_SESSION_TTL_SECONDS", 86400)) * time.Second,

		SlidingWindowSeconds:    time.Duration(getEnvAsInt("SLIDING_WINDOW_SECONDS", 30)) * time.Second,
		SlidingWindowMinSamples: getEnvAsInt("SLIDING_WINDOW_MIN_SAMPLES", 2),
		ThresholdTachycardiaBPM: getEnvAsFloat("THRESHOLD_TACHYCARDIA_BPM", 140.0),
		ThresholdBradycardiaBPM: getEnvAsFloat("THRESHOLD_BRADYCARDIA_BPM", 45.0),
		ThresholdHypoxemiaSpO2:  getEnvAsFloat("THRESHOLD_HYPOXEMIA_SPO2", 90.0),
		ThresholdCriticalSpO2:   getEnvAsFloat("THRESHOLD_CRITICAL_HYPOXEMIA_SPO2", 85.0),
		ThresholdSystolicBPHigh: getEnvAsFloat("THRESHOLD_SYSTOLIC_BP_HIGH", 180.0),
	}
}

func getEnv(key, defaultVal string) string {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		return val
	}
	return defaultVal
}

func getEnvAsInt(key string, defaultVal int) int {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		if intVal, err := strconv.Atoi(val); err == nil {
			return intVal
		}
	}
	return defaultVal
}

func getEnvAsFloat(key string, defaultVal float64) float64 {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		if fVal, err := strconv.ParseFloat(val, 64); err == nil {
			return fVal
		}
	}
	return defaultVal
}

func getEnvAsBool(key string, defaultVal bool) bool {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		if boolVal, err := strconv.ParseBool(val); err == nil {
			return boolVal
		}
	}
	return defaultVal
}
