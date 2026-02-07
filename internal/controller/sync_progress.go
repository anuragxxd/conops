package controller

import (
	"log/slog"
	"strings"
	"sync"
	"time"
)

const syncProgressFlushInterval = 2 * time.Second

type syncProgressReporter struct {
	registry *Registry
	logger   *slog.Logger
	appID    string
	interval time.Duration

	mu        sync.Mutex
	lastFlush time.Time
	lastValue string
}

func newSyncProgressReporter(registry *Registry, logger *slog.Logger, appID string, interval time.Duration) *syncProgressReporter {
	if interval <= 0 {
		interval = syncProgressFlushInterval
	}
	return &syncProgressReporter{
		registry: registry,
		logger:   logger,
		appID:    appID,
		interval: interval,
	}
}

func (r *syncProgressReporter) Update(value string) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return
	}

	now := time.Now()
	r.mu.Lock()
	r.lastValue = trimmed
	shouldFlush := r.lastFlush.IsZero() || now.Sub(r.lastFlush) >= r.interval
	if shouldFlush {
		r.lastFlush = now
	}
	r.mu.Unlock()

	if !shouldFlush {
		return
	}
	r.persist(trimmed, now)
}

func (r *syncProgressReporter) Flush() {
	r.mu.Lock()
	value := r.lastValue
	r.lastFlush = time.Now()
	r.mu.Unlock()

	if strings.TrimSpace(value) == "" {
		return
	}
	r.persist(value, time.Now())
}

func (r *syncProgressReporter) persist(value string, at time.Time) {
	if err := r.registry.UpdateSyncProgress(r.appID, at, value); err != nil && r.logger != nil {
		r.logger.Warn("Failed to persist sync progress", "app_id", r.appID, "error", err)
	}
}
