package order

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/fahrizaku/orderops/services/orders/internal/authn"
)

// fakeRepo is an in-memory Repository, including outbox semantics.
type fakeRepo struct {
	mu         sync.Mutex
	orders     map[int64]*Order
	outbox     []OutboxEvent
	sent       map[int64]bool
	nextID     int64
	nextOutbox int64
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{orders: map[int64]*Order{}, sent: map[int64]bool{}, nextID: 1, nextOutbox: 1}
}

func (f *fakeRepo) CreateWithOutbox(_ context.Context, o *Order) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	o.ID = f.nextID
	f.nextID++
	o.CreatedAt = time.Now()
	o.UpdatedAt = o.CreatedAt
	f.orders[o.ID] = o
	f.outbox = append(f.outbox, OutboxEvent{
		ID: f.nextOutbox,
		Payload: OrderCreatedEvent{
			OrderID: o.ID, CustomerID: o.CustomerID, StoreName: o.StoreName,
			Total: o.Total, Status: o.Status, OccurredAt: o.CreatedAt,
		},
	})
	f.nextOutbox++
	return nil
}

func (f *fakeRepo) ByID(_ context.Context, id int64) (*Order, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.orders[id]
	if !ok {
		return nil, ErrNotFound
	}
	return o, nil
}

func (f *fakeRepo) ListByCustomer(_ context.Context, customerID int64, _, _ int) ([]*Order, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*Order
	for _, o := range f.orders {
		if o.CustomerID == customerID {
			out = append(out, o)
		}
	}
	return out, nil
}

func (f *fakeRepo) ListAll(_ context.Context, _, _ int) ([]*Order, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*Order, 0, len(f.orders))
	for _, o := range f.orders {
		out = append(out, o)
	}
	return out, nil
}

func (f *fakeRepo) UpdateStatus(_ context.Context, id int64, status Status, courierID *int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.orders[id]
	if !ok {
		return ErrNotFound
	}
	o.Status = status
	if courierID != nil {
		o.CourierID = courierID
	}
	return nil
}

func (f *fakeRepo) PendingOutbox(_ context.Context, limit int) ([]OutboxEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []OutboxEvent
	for _, ev := range f.outbox {
		if !f.sent[ev.ID] {
			ev.Payload.EventID = ev.ID
			out = append(out, ev)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (f *fakeRepo) MarkOutboxSent(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent[id] = true
	return nil
}

var (
	customer  = authn.Caller{ID: 7, Role: "customer"}
	stranger  = authn.Caller{ID: 8, Role: "customer"}
	courier   = authn.Caller{ID: 21, Role: "courier"}
	validCart = CreateInput{
		StoreName: "Warung Sederhana",
		Address:   "Jl. Merdeka No. 1",
		Items: []Item{
			{Name: "Nasi Goreng", Qty: 2, Price: 15000},
			{Name: "Es Teh", Qty: 1, Price: 5000},
		},
		DeliveryFee: 7000,
	}
)

func newTestService() (*Service, *fakeRepo) {
	repo := newFakeRepo()
	return NewService(repo, slog.Default()), repo
}

func TestCreateComputesTotalsAndQueuesEvent(t *testing.T) {
	svc, repo := newTestService()
	o, err := svc.Create(context.Background(), customer, validCart)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if o.Subtotal != 35000 {
		t.Errorf("subtotal = %d, want 35000", o.Subtotal)
	}
	if o.Total != 42000 {
		t.Errorf("total = %d, want 42000", o.Total)
	}
	if o.Status != StatusPending {
		t.Errorf("status = %q, want pending", o.Status)
	}
	pending, err := repo.PendingOutbox(context.Background(), 10)
	if err != nil {
		t.Fatalf("pending outbox: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending events = %d, want 1", len(pending))
	}
	if pending[0].Payload.OrderID != o.ID || pending[0].Payload.Total != 42000 {
		t.Errorf("event payload = %+v, want order %d total 42000", pending[0].Payload, o.ID)
	}
}

func TestCreateValidation(t *testing.T) {
	svc, _ := newTestService()
	cases := map[string]CreateInput{
		"no store":     {Address: "Jl. X", Items: validCart.Items},
		"no address":   {StoreName: "Warteg", Items: validCart.Items},
		"no items":     {StoreName: "Warteg", Address: "Jl. X"},
		"zero qty":     {StoreName: "Warteg", Address: "Jl. X", Items: []Item{{Name: "A", Qty: 0, Price: 1000}}},
		"negative fee": {StoreName: "Warteg", Address: "Jl. X", Items: validCart.Items, DeliveryFee: -1},
	}
	for name, in := range cases {
		if _, err := svc.Create(context.Background(), customer, in); !errors.Is(err, ErrValidation) {
			t.Errorf("%s: err = %v, want ErrValidation", name, err)
		}
	}
}

func TestGetPermissions(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()
	o, err := svc.Create(ctx, customer, validCart)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.Get(ctx, customer, o.ID); err != nil {
		t.Errorf("owner get: %v", err)
	}
	if _, err := svc.Get(ctx, stranger, o.ID); !errors.Is(err, ErrForbidden) {
		t.Errorf("stranger get err = %v, want ErrForbidden", err)
	}
	if _, err := svc.Get(ctx, courier, o.ID); err != nil {
		t.Errorf("courier get: %v", err)
	}
}

func TestListScoping(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()
	if _, err := svc.Create(ctx, customer, validCart); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.Create(ctx, stranger, validCart); err != nil {
		t.Fatalf("create: %v", err)
	}
	own, err := svc.List(ctx, customer, 20, 0)
	if err != nil || len(own) != 1 {
		t.Errorf("customer list = %d orders, err %v; want 1 order", len(own), err)
	}
	all, err := svc.List(ctx, courier, 20, 0)
	if err != nil || len(all) != 2 {
		t.Errorf("courier list = %d orders, err %v; want 2 orders", len(all), err)
	}
}

func TestStatusFlow(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()
	o, err := svc.Create(ctx, customer, validCart)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := svc.UpdateStatus(ctx, customer, o.ID, StatusConfirmed); !errors.Is(err, ErrForbidden) {
		t.Errorf("customer update err = %v, want ErrForbidden", err)
	}
	if _, err := svc.UpdateStatus(ctx, courier, o.ID, StatusCompleted); !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("pending->completed err = %v, want ErrInvalidTransition", err)
	}
	if _, err := svc.UpdateStatus(ctx, courier, o.ID, StatusConfirmed); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	updated, err := svc.UpdateStatus(ctx, courier, o.ID, StatusDelivering)
	if err != nil {
		t.Fatalf("delivering: %v", err)
	}
	if updated.CourierID == nil || *updated.CourierID != courier.ID {
		t.Errorf("courier_id = %v, want %d", updated.CourierID, courier.ID)
	}
	if _, err := svc.UpdateStatus(ctx, courier, o.ID, StatusCompleted); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if _, err := svc.UpdateStatus(ctx, courier, o.ID, StatusCancelled); !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("completed->cancelled err = %v, want ErrInvalidTransition", err)
	}
}

// fakePublisher records what the relay sends and can be told to fail.
type fakePublisher struct {
	mu     sync.Mutex
	sent   []OrderCreatedEvent
	failOn bool
}

func (f *fakePublisher) Publish(_ context.Context, ev OrderCreatedEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failOn {
		return errors.New("broker unreachable")
	}
	f.sent = append(f.sent, ev)
	return nil
}

func TestRelayPublishesAndMarksSent(t *testing.T) {
	svc, repo := newTestService()
	ctx := context.Background()
	if _, err := svc.Create(ctx, customer, validCart); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.Create(ctx, stranger, validCart); err != nil {
		t.Fatalf("create: %v", err)
	}
	pub := &fakePublisher{}
	relay := NewRelay(repo, pub, slog.Default(), time.Second)
	relay.Flush(ctx)
	if len(pub.sent) != 2 {
		t.Fatalf("published = %d, want 2", len(pub.sent))
	}
	if pub.sent[0].OrderID > pub.sent[1].OrderID {
		t.Error("events must be published in insertion order")
	}
	pending, _ := repo.PendingOutbox(ctx, 10)
	if len(pending) != 0 {
		t.Errorf("pending after flush = %d, want 0", len(pending))
	}
}

func TestRelayLeavesPendingOnFailure(t *testing.T) {
	svc, repo := newTestService()
	ctx := context.Background()
	if _, err := svc.Create(ctx, customer, validCart); err != nil {
		t.Fatalf("create: %v", err)
	}
	pub := &fakePublisher{failOn: true}
	relay := NewRelay(repo, pub, slog.Default(), time.Second)
	relay.Flush(ctx)
	pending, _ := repo.PendingOutbox(ctx, 10)
	if len(pending) != 1 {
		t.Fatalf("pending after failed flush = %d, want 1 (kept for retry)", len(pending))
	}
	pub.failOn = false
	relay.Flush(ctx)
	pending, _ = repo.PendingOutbox(ctx, 10)
	if len(pending) != 0 {
		t.Errorf("pending after retry = %d, want 0", len(pending))
	}
}
