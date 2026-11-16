package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// Service exposes the audit log for reading. List answers are cached in
// Redis for a short TTL: the audit feed is the platform's hottest read
// and a few seconds of staleness is acceptable there, while writes (the
// consumer) always go straight to the store.
type Service struct {
	store Store
	cache Cache // may be nil
	ttl   time.Duration
	log   *slog.Logger
}

func NewService(store Store, cache Cache, ttl time.Duration, log *slog.Logger) *Service {
	return &Service{store: store, cache: cache, ttl: ttl, log: log}
}

func (s *Service) List(ctx context.Context, limit, offset int) ([]Entry, error) {
	key := fmt.Sprintf("audit:list:%d:%d", limit, offset)
	if s.cache != nil {
		raw, err := s.cache.Get(ctx, key)
		if err == nil {
			var entries []Entry
			if json.Unmarshal([]byte(raw), &entries) == nil {
				return entries, nil
			}
		} else if err != ErrCacheMiss {
			// A cache outage must never break the read path.
			s.log.Warn("cache read failed, falling back to store", "err", err)
		}
	}
	entries, err := s.store.List(ctx, int64(limit), int64(offset))
	if err != nil {
		return nil, err
	}
	if s.cache != nil {
		if raw, err := json.Marshal(entries); err == nil {
			if err := s.cache.Set(ctx, key, string(raw), s.ttl); err != nil {
				s.log.Warn("cache write failed", "err", err)
			}
		}
	}
	return entries, nil
}
