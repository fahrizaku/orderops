package order

import "time"

// Status is the lifecycle state of an order.
type Status string

const (
	StatusPending    Status = "pending"
	StatusConfirmed  Status = "confirmed"
	StatusDelivering Status = "delivering"
	StatusCompleted  Status = "completed"
	StatusCancelled  Status = "cancelled"
)

// ValidTransition reports whether an order may move from one status to
// another. Completed and cancelled are terminal.
func ValidTransition(from, to Status) bool {
	switch from {
	case StatusPending:
		return to == StatusConfirmed || to == StatusCancelled
	case StatusConfirmed:
		return to == StatusDelivering || to == StatusCancelled
	case StatusDelivering:
		return to == StatusCompleted
	}
	return false
}

// Item is one line of an order. Price is an integer amount in rupiah —
// money is never stored or computed as a float.
type Item struct {
	Name  string `json:"name"`
	Qty   int    `json:"qty"`
	Price int64  `json:"price"`
}

// Order is one customer order with its items.
type Order struct {
	ID          int64     `json:"id"`
	CustomerID  int64     `json:"customer_id"`
	StoreName   string    `json:"store_name"`
	Address     string    `json:"address"`
	Items       []Item    `json:"items"`
	Subtotal    int64     `json:"subtotal"`
	DeliveryFee int64     `json:"delivery_fee"`
	Total       int64     `json:"total"`
	Status      Status    `json:"status"`
	CourierID   *int64    `json:"courier_id,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// OrderCreatedEvent is published to RabbitMQ (exchange "orders", routing
// key "order.created") whenever a new order is stored.
type OrderCreatedEvent struct {
	EventID    int64     `json:"event_id"`
	OrderID    int64     `json:"order_id"`
	CustomerID int64     `json:"customer_id"`
	StoreName  string    `json:"store_name"`
	Total      int64     `json:"total"`
	Status     Status    `json:"status"`
	OccurredAt time.Time `json:"occurred_at"`
}

// OutboxEvent is one row of the transactional outbox: the event payload
// waiting to be (or already) published.
type OutboxEvent struct {
	ID      int64
	Payload OrderCreatedEvent
}
