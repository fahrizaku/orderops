package order

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/fahrizaku/orderops/services/orders/internal/authn"
)

var (
	ErrValidation        = errors.New("validation failed")
	ErrForbidden         = errors.New("forbidden")
	ErrInvalidTransition = errors.New("invalid status transition")
)

// Service holds the orders business logic. Callers are identified by the
// JWT the auth service issued; the service enforces who may see and move
// an order based on the caller's role.
type Service struct {
	repo Repository
	log  *slog.Logger
}

func NewService(repo Repository, log *slog.Logger) *Service {
	return &Service{repo: repo, log: log}
}

type CreateInput struct {
	StoreName   string
	Address     string
	Items       []Item
	DeliveryFee int64
}

// Create validates the input, computes totals server-side (client-sent
// prices are per-item catalogue prices; the totals are never trusted from
// the request) and stores the order together with its outbox event.
func (s *Service) Create(ctx context.Context, caller authn.Caller, in CreateInput) (*Order, error) {
	if err := validateCreate(in); err != nil {
		return nil, err
	}
	var subtotal int64
	for _, it := range in.Items {
		subtotal += int64(it.Qty) * it.Price
	}
	o := &Order{
		CustomerID:  caller.ID,
		StoreName:   strings.TrimSpace(in.StoreName),
		Address:     strings.TrimSpace(in.Address),
		Items:       in.Items,
		Subtotal:    subtotal,
		DeliveryFee: in.DeliveryFee,
		Total:       subtotal + in.DeliveryFee,
		Status:      StatusPending,
	}
	if err := s.repo.CreateWithOutbox(ctx, o); err != nil {
		return nil, err
	}
	s.log.Info("order created", "order_id", o.ID, "customer_id", o.CustomerID, "total", o.Total)
	return o, nil
}

func validateCreate(in CreateInput) error {
	if strings.TrimSpace(in.StoreName) == "" {
		return fmt.Errorf("%w: store_name is required", ErrValidation)
	}
	if strings.TrimSpace(in.Address) == "" {
		return fmt.Errorf("%w: address is required", ErrValidation)
	}
	if len(in.Items) == 0 {
		return fmt.Errorf("%w: at least one item is required", ErrValidation)
	}
	for _, it := range in.Items {
		if strings.TrimSpace(it.Name) == "" {
			return fmt.Errorf("%w: item name is required", ErrValidation)
		}
		if it.Qty <= 0 {
			return fmt.Errorf("%w: item qty must be positive", ErrValidation)
		}
		if it.Price < 0 {
			return fmt.Errorf("%w: item price must not be negative", ErrValidation)
		}
	}
	if in.DeliveryFee < 0 {
		return fmt.Errorf("%w: delivery_fee must not be negative", ErrValidation)
	}
	return nil
}

// Get returns one order. Customers may only see their own orders; couriers
// and admins may see any.
func (s *Service) Get(ctx context.Context, caller authn.Caller, id int64) (*Order, error) {
	o, err := s.repo.ByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if !caller.IsStaff() && o.CustomerID != caller.ID {
		return nil, ErrForbidden
	}
	return o, nil
}

// List returns the caller's own orders, or all orders for staff.
func (s *Service) List(ctx context.Context, caller authn.Caller, limit, offset int) ([]*Order, error) {
	if caller.IsStaff() {
		return s.repo.ListAll(ctx, limit, offset)
	}
	return s.repo.ListByCustomer(ctx, caller.ID, limit, offset)
}

// UpdateStatus moves an order along its lifecycle. Only staff may do this,
// transitions must follow ValidTransition, and a courier who starts a
// delivery is recorded as the order's courier.
func (s *Service) UpdateStatus(ctx context.Context, caller authn.Caller, id int64, to Status) (*Order, error) {
	if !caller.IsStaff() {
		return nil, ErrForbidden
	}
	o, err := s.repo.ByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if !ValidTransition(o.Status, to) {
		return nil, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, o.Status, to)
	}
	var courierID *int64
	if to == StatusDelivering && caller.Role == "courier" {
		courierID = &caller.ID
	}
	if err := s.repo.UpdateStatus(ctx, id, to, courierID); err != nil {
		return nil, err
	}
	o.Status = to
	if courierID != nil {
		o.CourierID = courierID
	}
	s.log.Info("order status updated", "order_id", id, "status", to, "by", caller.ID)
	return o, nil
}
