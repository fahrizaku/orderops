package audit

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"
)

// Handler exposes the audit log over HTTP/JSON. The read endpoint is
// rate-limited per client; health endpoints are not.
type Handler struct {
	svc     *Service
	limiter Limiter // may be nil
	log     *slog.Logger
	pingers []pinger
}

type pinger interface {
	Ping(ctx context.Context) error
}

func NewHandler(svc *Service, limiter Limiter, log *slog.Logger, pingers ...pinger) *Handler {
	return &Handler{svc: svc, limiter: limiter, log: log, pingers: pingers}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", h.readyz)
	mux.Handle("GET /audit", h.rateLimited(http.HandlerFunc(h.list)))
}

func (h *Handler) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	for _, p := range h.pingers {
		if err := p.Ping(ctx); err != nil {
			writeError(w, http.StatusServiceUnavailable, "dependency not reachable")
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	limit, offset := pageParams(r)
	entries, err := h.svc.List(r.Context(), limit, offset)
	if err != nil {
		h.log.Error("list audit entries", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

// rateLimited rejects requests beyond the per-client budget with 429.
// Limiter errors fail open: a Redis outage should degrade the limit, not
// take the read API down with it.
func (h *Handler) rateLimited(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.limiter != nil {
			allowed, err := h.limiter.Allow(r.Context(), clientKey(r))
			switch {
			case err != nil:
				h.log.Warn("rate limiter unavailable, failing open", "err", err)
			case !allowed:
				writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func clientKey(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func pageParams(r *http.Request) (limit, offset int) {
	limit, offset = 20, 0
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v <= 100 {
		limit = v
	}
	if v, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && v >= 0 {
		offset = v
	}
	return limit, offset
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
