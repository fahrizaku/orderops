package audit

import "time"

// OrderCreatedEvent mirrors the payload the orders service publishes to
// the "orders" exchange with routing key "order.created". Its EventID is
// the orders service's outbox row id, unique per event, which makes it a
// natural idempotency key on this side.
type OrderCreatedEvent struct {
	EventID    int64     `json:"event_id"`
	OrderID    int64     `json:"order_id"`
	CustomerID int64     `json:"customer_id"`
	StoreName  string    `json:"store_name"`
	Total      int64     `json:"total"`
	Status     string    `json:"status"`
	OccurredAt time.Time `json:"occurred_at"`
}

// Entry is one audit-log record: the event as it arrived, plus when this
// service received it.
type Entry struct {
	EventID    int64     `bson:"event_id" json:"event_id"`
	OrderID    int64     `bson:"order_id" json:"order_id"`
	CustomerID int64     `bson:"customer_id" json:"customer_id"`
	StoreName  string    `bson:"store_name" json:"store_name"`
	Total      int64     `bson:"total" json:"total"`
	Status     string    `bson:"status" json:"status"`
	OccurredAt time.Time `bson:"occurred_at" json:"occurred_at"`
	ReceivedAt time.Time `bson:"received_at" json:"received_at"`
}
