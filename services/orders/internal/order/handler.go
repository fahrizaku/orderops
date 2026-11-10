package order

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fahrizaku/orderops/services/orders/internal/authn"
)

// Handler exposes the orders service over HTTP/JSON. Every order endpoint
// requires a JWT issued by the auth service.
type Handler struct {
	svc      *Service
	verifier *authn.Verifier
	log      *slog.Logger
	pool     *pgxpool.Pool
}

func NewHandler(svc *Service, verifier *authn.Verifier, log *slog.Logger, pool *pgxpool.Pool) *Handler {
	return &Handler{svc: svc, verifier: verifier, log: log, pool: pool}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.Handle("POST /orders", h.requireAuth(http.HandlerFunc(h.create)))
	mux.Handle("GET /orders", h.requireAuth(http.HandlerFunc(h.list)))
	mux.Handle("GET /orders/{id}", h.requireAuth(http.HandlerFunc(h.get)))
	mux.Handle("PATCH /orders/{id}/status", h.requireAuth(http.HandlerFunc(h.updateStatus)))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", h.readyz)
}

func (h *Handler) readyz(w http.ResponseWriter, r *http.Request) {
	if err := h.pool.Ping(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database not reachable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

type createRequest struct {
	StoreName   string `json:"store_name"`
	Address     string `json:"address"`
	Items       []Item `json:"items"`
	DeliveryFee int64  `json:"delivery_fee"`
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "request body must be valid JSON")
		return
	}
	o, err := h.svc.Create(r.Context(), callerOf(r), CreateInput{
		StoreName: req.StoreName, Address: req.Address,
		Items: req.Items, DeliveryFee: req.DeliveryFee,
	})
	h.respond(w, http.StatusCreated, o, err, "create order")
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	limit, offset := pageParams(r)
	orders, err := h.svc.List(r.Context(), callerOf(r), limit, offset)
	if err != nil {
		h.fail(w, err, "list orders")
		return
	}
	if orders == nil {
		orders = []*Order{}
	}
	writeJSON(w, http.StatusOK, orders)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	o, err := h.svc.Get(r.Context(), callerOf(r), id)
	h.respond(w, http.StatusOK, o, err, "get order")
}

type statusRequest struct {
	Status Status `json:"status"`
}

func (h *Handler) updateStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req statusRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "request body must be valid JSON")
		return
	}
	o, err := h.svc.UpdateStatus(r.Context(), callerOf(r), id, req.Status)
	h.respond(w, http.StatusOK, o, err, "update order status")
}

// respond writes o on success, or maps a service error to a status code.
func (h *Handler) respond(w http.ResponseWriter, status int, o *Order, err error, op string) {
	if err != nil {
		h.fail(w, err, op)
		return
	}
	writeJSON(w, status, o)
}

func (h *Handler) fail(w http.ResponseWriter, err error, op string) {
	switch {
	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrForbidden):
		writeError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrInvalidTransition):
		writeError(w, http.StatusConflict, err.Error())
	default:
		h.log.Error("request failed", "op", op, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

type callerKey struct{}

func callerOf(r *http.Request) authn.Caller {
	c, _ := r.Context().Value(callerKey{}).(authn.Caller)
	return c
}

func (h *Handler) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || token == "" {
			writeError(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		caller, err := h.verifier.Verify(token)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid or expired token")
			return
		}
		ctx := context.WithValue(r.Context(), callerKey{}, caller)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "order id must be a number")
		return 0, false
	}
	return id, true
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

func decodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
