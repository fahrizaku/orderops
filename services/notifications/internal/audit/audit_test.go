package audit

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// fakeStore is an in-memory Store enforcing the unique event_id rule.
type fakeStore struct {
	mu        sync.Mutex
	entries   map[int64]Entry
	listCalls int
	saveErr   error
}

func newFakeStore() *fakeStore {
	return &fakeStore{entries: map[int64]Entry{}}
}

func (f *fakeStore) Save(_ context.Context, e Entry) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.saveErr != nil {
		return false, f.saveErr
	}
	if _, dup := f.entries[e.EventID]; dup {
		return false, nil
	}
	f.entries[e.EventID] = e
	return true, nil
}

func (f *fakeStore) List(_ context.Context, limit, offset int64) ([]Entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls++
	out := []Entry{}
	for _, e := range f.entries {
		out = append(out, e)
	}
	if int64(len(out)) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeStore) Ping(context.Context) error { return nil }

func (f *fakeStore) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.entries)
}

var sampleEvent = OrderCreatedEvent{
	EventID:    11,
	OrderID:    5,
	CustomerID: 7,
	StoreName:  "Warung Sederhana",
	Total:      42000,
	Status:     "pending",
	OccurredAt: time.Now(),
}

func bodyOf(t *testing.T, ev OrderCreatedEvent) []byte {
	t.Helper()
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	return b
}

func TestHandleStoresEntry(t *testing.T) {
	store := newFakeStore()
	h := NewEventHandler(store, slog.Default())
	if err := h.Handle(context.Background(), bodyOf(t, sampleEvent)); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if store.count() != 1 {
		t.Fatalf("stored entries = %d, want 1", store.count())
	}
	got := store.entries[sampleEvent.EventID]
	if got.OrderID != sampleEvent.OrderID || got.Total != sampleEvent.Total || got.StoreName != sampleEvent.StoreName {
		t.Errorf("stored entry = %+v, want event fields copied through", got)
	}
	if got.ReceivedAt.IsZero() {
		t.Error("received_at must be stamped on receipt")
	}
}

func TestHandleDedupesRedelivery(t *testing.T) {
	store := newFakeStore()
	h := NewEventHandler(store, slog.Default())
	body := bodyOf(t, sampleEvent)
	ctx := context.Background()
	if err := h.Handle(ctx, body); err != nil {
		t.Fatalf("first handle: %v", err)
	}
	// At-least-once delivery means the same event can arrive again; it
	// must be acknowledged without creating a second audit entry.
	if err := h.Handle(ctx, body); err != nil {
		t.Fatalf("redelivery handle: %v", err)
	}
	if store.count() != 1 {
		t.Errorf("stored entries after redelivery = %d, want 1", store.count())
	}
}

func TestHandlePoisonMessages(t *testing.T) {
	store := newFakeStore()
	h := NewEventHandler(store, slog.Default())
	if err := h.Handle(context.Background(), []byte("not json")); !errors.Is(err, ErrPoison) {
		t.Errorf("bad json err = %v, want ErrPoison", err)
	}
	noID := sampleEvent
	noID.EventID = 0
	if err := h.Handle(context.Background(), bodyOf(t, noID)); !errors.Is(err, ErrPoison) {
		t.Errorf("missing event_id err = %v, want ErrPoison", err)
	}
	if store.count() != 0 {
		t.Errorf("poison messages stored %d entries, want 0", store.count())
	}
}

func TestHandlePropagatesStoreErrors(t *testing.T) {
	store := newFakeStore()
	store.saveErr = errors.New("mongo unreachable")
	h := NewEventHandler(store, slog.Default())
	err := h.Handle(context.Background(), bodyOf(t, sampleEvent))
	if err == nil || errors.Is(err, ErrPoison) {
		t.Errorf("err = %v, want a plain (requeueable) store error", err)
	}
}

// fakeCache is an in-memory Cache that can be told to fail.
type fakeCache struct {
	mu   sync.Mutex
	data map[string]string
	err  error
}

func newFakeCache() *fakeCache { return &fakeCache{data: map[string]string{}} }

func (f *fakeCache) Get(_ context.Context, key string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return "", f.err
	}
	v, ok := f.data[key]
	if !ok {
		return "", ErrCacheMiss
	}
	return v, nil
}

func (f *fakeCache) Set(_ context.Context, key, value string, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.data[key] = value
	return nil
}

func (f *fakeCache) Ping(context.Context) error { return nil }

func TestListCachesStoreResults(t *testing.T) {
	store := newFakeStore()
	if _, err := store.Save(context.Background(), Entry{EventID: 1, OrderID: 1}); err != nil {
		t.Fatalf("seed store: %v", err)
	}
	svc := NewService(store, newFakeCache(), time.Minute, slog.Default())
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		entries, err := svc.List(ctx, 20, 0)
		if err != nil {
			t.Fatalf("list %d: %v", i, err)
		}
		if len(entries) != 1 {
			t.Fatalf("list %d returned %d entries, want 1", i, len(entries))
		}
	}
	if store.listCalls != 1 {
		t.Errorf("store was hit %d times for 3 identical lists, want 1 (rest from cache)", store.listCalls)
	}
}

func TestListSurvivesCacheOutage(t *testing.T) {
	store := newFakeStore()
	if _, err := store.Save(context.Background(), Entry{EventID: 1, OrderID: 1}); err != nil {
		t.Fatalf("seed store: %v", err)
	}
	cache := newFakeCache()
	cache.err = errors.New("redis unreachable")
	svc := NewService(store, cache, time.Minute, slog.Default())
	entries, err := svc.List(context.Background(), 20, 0)
	if err != nil {
		t.Fatalf("list with broken cache: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("entries = %d, want 1 (served from store)", len(entries))
	}
}

// fakeLimiter is a programmable Limiter.
type fakeLimiter struct {
	allowed bool
	err     error
}

func (f fakeLimiter) Allow(context.Context, string) (bool, error) {
	return f.allowed, f.err
}

func auditRequest() *http.Request {
	return httptest.NewRequest(http.MethodGet, "/audit", nil)
}

func TestRateLimitMiddleware(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store, nil, time.Minute, slog.Default())
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	cases := []struct {
		name    string
		limiter Limiter
		want    int
	}{
		{"allowed", fakeLimiter{allowed: true}, http.StatusOK},
		{"denied", fakeLimiter{allowed: false}, http.StatusTooManyRequests},
		{"limiter error fails open", fakeLimiter{err: errors.New("redis down")}, http.StatusOK},
	}
	for _, tc := range cases {
		h := NewHandler(svc, tc.limiter, slog.Default())
		rec := httptest.NewRecorder()
		h.rateLimited(ok).ServeHTTP(rec, auditRequest())
		if rec.Code != tc.want {
			t.Errorf("%s: status = %d, want %d", tc.name, rec.Code, tc.want)
		}
	}
}

func TestListEndpoint(t *testing.T) {
	store := newFakeStore()
	if _, err := store.Save(context.Background(), Entry{EventID: 3, OrderID: 9, StoreName: "Warteg"}); err != nil {
		t.Fatalf("seed store: %v", err)
	}
	svc := NewService(store, nil, time.Minute, slog.Default())
	h := NewHandler(svc, nil, slog.Default())
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, auditRequest())
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /audit status = %d, want 200", rec.Code)
	}
	var entries []Entry
	if err := json.Unmarshal(rec.Body.Bytes(), &entries); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(entries) != 1 || entries[0].OrderID != 9 {
		t.Errorf("response = %+v, want the seeded entry", entries)
	}
}
