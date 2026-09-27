package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/opsmaster/app-services/pkg/config"
	"github.com/opsmaster/app-services/pkg/metrics"
	"github.com/redis/go-redis/v9"
)

type TelehealthSession struct {
	ID                string                    `json:"id"`
	PatientID         string                    `json:"patient_id"`
	DoctorID          string                    `json:"doctor_id"`
	Status            string                    `json:"status"` // WAITING, CONNECTED, TERMINATED
	CreatedAt         time.Time                 `json:"created_at"`
	UpdatedAt         time.Time                 `json:"updated_at"`
	EndedAt           *time.Time                `json:"ended_at,omitempty"`
	TerminationReason string                    `json:"termination_reason,omitempty"`
	Peers             map[string]PeerConnection `json:"peers"`
	Metadata          map[string]string         `json:"metadata,omitempty"`
}

type PeerConnection struct {
	Role     string    `json:"role"`
	JoinedAt time.Time `json:"joined_at"`
}

type Cache interface {
	PushVitalStream(ctx context.Context, stream string, data map[string]interface{}) error
	PublishEvent(ctx context.Context, channel string, message interface{}) error
	SaveSession(ctx context.Context, session *TelehealthSession) error
	GetSession(ctx context.Context, sessionID string) (*TelehealthSession, error)
	DeleteSession(ctx context.Context, sessionID string) error
	ListSessions(ctx context.Context) ([]*TelehealthSession, error)
	Ping(ctx context.Context) error
	Close() error
}

type RedisCache struct {
	client       *redis.Client
	keyPrefix    string
	sessionTTL   time.Duration
	mockFallback bool
	mu           sync.RWMutex
	inMemStore   map[string]string
}

func New(cfg *config.Config) (Cache, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:         cfg.RedisAddr,
		Password:     cfg.RedisPassword,
		DB:           cfg.RedisDB,
		DialTimeout:  2 * time.Second,
		ReadTimeout:  1 * time.Second,
		WriteTimeout: 1 * time.Second,
		PoolSize:     50,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		if cfg.EnableMockFallback {
			slog.Warn("Redis unavailable, using in-memory mock cache fallback",
				slog.String("addr", cfg.RedisAddr),
				slog.String("error", err.Error()),
			)
			return &RedisCache{
				keyPrefix:    cfg.RedisSessionPrefix,
				sessionTTL:   cfg.RedisSessionTTL,
				mockFallback: true,
				inMemStore:   make(map[string]string),
			}, nil
		}
		return nil, fmt.Errorf("failed to connect to redis: %w", err)
	}

	slog.Info("Connected to Redis successfully", slog.String("addr", cfg.RedisAddr))

	return &RedisCache{
		client:       rdb,
		keyPrefix:    cfg.RedisSessionPrefix,
		sessionTTL:   cfg.RedisSessionTTL,
		mockFallback: false,
	}, nil
}

func (r *RedisCache) PushVitalStream(ctx context.Context, stream string, data map[string]interface{}) error {
	start := time.Now()
	defer func() {
		metrics.QueueLatency.Observe(time.Since(start).Seconds())
	}()

	if r.mockFallback {
		return nil
	}

	args := &redis.XAddArgs{
		Stream: stream,
		MaxLen: 100000,
		Approx: true,
		Values: data,
	}
	return r.client.XAdd(ctx, args).Err()
}

func (r *RedisCache) PublishEvent(ctx context.Context, channel string, message interface{}) error {
	if r.mockFallback {
		return nil
	}

	raw, err := json.Marshal(message)
	if err != nil {
		return err
	}
	return r.client.Publish(ctx, channel, raw).Err()
}

func (r *RedisCache) SaveSession(ctx context.Context, session *TelehealthSession) error {
	raw, err := json.Marshal(session)
	if err != nil {
		return err
	}

	if r.mockFallback {
		r.mu.Lock()
		r.inMemStore[session.ID] = string(raw)
		r.mu.Unlock()
		return nil
	}

	key := r.keyPrefix + session.ID
	return r.client.Set(ctx, key, raw, r.sessionTTL).Err()
}

func (r *RedisCache) GetSession(ctx context.Context, sessionID string) (*TelehealthSession, error) {
	if r.mockFallback {
		r.mu.RLock()
		data, ok := r.inMemStore[sessionID]
		r.mu.RUnlock()
		if !ok {
			return nil, nil
		}
		var s TelehealthSession
		if err := json.Unmarshal([]byte(data), &s); err != nil {
			return nil, err
		}
		return &s, nil
	}

	key := r.keyPrefix + sessionID
	val, err := r.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var session TelehealthSession
	if err := json.Unmarshal([]byte(val), &session); err != nil {
		return nil, err
	}
	return &session, nil
}

func (r *RedisCache) DeleteSession(ctx context.Context, sessionID string) error {
	if r.mockFallback {
		r.mu.Lock()
		delete(r.inMemStore, sessionID)
		r.mu.Unlock()
		return nil
	}

	key := r.keyPrefix + sessionID
	return r.client.Del(ctx, key).Err()
}

func (r *RedisCache) ListSessions(ctx context.Context) ([]*TelehealthSession, error) {
	if r.mockFallback {
		r.mu.RLock()
		defer r.mu.RUnlock()
		sessions := make([]*TelehealthSession, 0, len(r.inMemStore))
		for _, data := range r.inMemStore {
			var s TelehealthSession
			if err := json.Unmarshal([]byte(data), &s); err == nil {
				sessions = append(sessions, &s)
			}
		}
		return sessions, nil
	}

	keys, err := r.client.Keys(ctx, r.keyPrefix+"*").Result()
	if err != nil {
		return nil, err
	}

	sessions := make([]*TelehealthSession, 0, len(keys))
	for _, key := range keys {
		val, err := r.client.Get(ctx, key).Result()
		if err == nil {
			var s TelehealthSession
			if err := json.Unmarshal([]byte(val), &s); err == nil {
				sessions = append(sessions, &s)
			}
		}
	}
	return sessions, nil
}

func (r *RedisCache) Ping(ctx context.Context) error {
	if r.mockFallback {
		return nil
	}
	return r.client.Ping(ctx).Err()
}

func (r *RedisCache) Close() error {
	if r.client != nil {
		return r.client.Close()
	}
	return nil
}
