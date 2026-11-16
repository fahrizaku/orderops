package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// ErrPoison marks a message that can never be processed (undecodable, or
// missing its idempotency key). Poison messages are acknowledged and
// dropped: redelivering them would loop forever.
var ErrPoison = errors.New("unprocessable message")

// EventHandler turns one raw message body into an audit entry. It is the
// testable core of the consumer, separated from the AMQP plumbing.
type EventHandler struct {
	store Store
	log   *slog.Logger
}

func NewEventHandler(store Store, log *slog.Logger) *EventHandler {
	return &EventHandler{store: store, log: log}
}

func (h *EventHandler) Handle(ctx context.Context, body []byte) error {
	var ev OrderCreatedEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		return fmt.Errorf("%w: %v", ErrPoison, err)
	}
	if ev.EventID == 0 {
		return fmt.Errorf("%w: missing event_id", ErrPoison)
	}
	inserted, err := h.store.Save(ctx, Entry{
		EventID:    ev.EventID,
		OrderID:    ev.OrderID,
		CustomerID: ev.CustomerID,
		StoreName:  ev.StoreName,
		Total:      ev.Total,
		Status:     ev.Status,
		OccurredAt: ev.OccurredAt,
		ReceivedAt: time.Now(),
	})
	if err != nil {
		return err
	}
	if inserted {
		h.log.Info("audit entry recorded", "event_id", ev.EventID, "order_id", ev.OrderID)
	} else {
		h.log.Info("duplicate event skipped", "event_id", ev.EventID)
	}
	return nil
}

// Consumer subscribes the durable queue "notifications.audit" to the
// "orders" topic exchange for order.created events. It reconnects with a
// fixed delay whenever the broker connection drops, and delivery is
// at-least-once: messages are acked only after the audit entry is stored,
// and the store's unique event_id index absorbs any redelivery.
type Consumer struct {
	url     string
	handler *EventHandler
	log     *slog.Logger
}

func NewConsumer(url string, handler *EventHandler, log *slog.Logger) *Consumer {
	return &Consumer{url: url, handler: handler, log: log}
}

func (c *Consumer) Run(ctx context.Context) {
	for {
		if err := c.consume(ctx); err != nil && ctx.Err() == nil {
			c.log.Warn("consumer stopped, reconnecting", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
}

func (c *Consumer) consume(ctx context.Context) error {
	conn, err := amqp.Dial(c.url)
	if err != nil {
		return err
	}
	defer conn.Close()

	ch, err := conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()

	if err := ch.ExchangeDeclare("orders", "topic", true, false, false, false, nil); err != nil {
		return err
	}
	queue, err := ch.QueueDeclare("notifications.audit", true, false, false, false, nil)
	if err != nil {
		return err
	}
	if err := ch.QueueBind(queue.Name, "order.created", "orders", false, nil); err != nil {
		return err
	}
	if err := ch.Qos(10, 0, false); err != nil {
		return err
	}
	deliveries, err := ch.Consume(queue.Name, "", false, false, false, false, nil)
	if err != nil {
		return err
	}
	c.log.Info("consuming order.created events", "queue", queue.Name)

	for {
		select {
		case <-ctx.Done():
			return nil
		case d, ok := <-deliveries:
			if !ok {
				return errors.New("delivery channel closed")
			}
			c.deliver(ctx, d)
		}
	}
}

func (c *Consumer) deliver(ctx context.Context, d amqp.Delivery) {
	err := c.handler.Handle(ctx, d.Body)
	switch {
	case err == nil:
		_ = d.Ack(false)
	case errors.Is(err, ErrPoison):
		c.log.Warn("dropping poison message", "err", err)
		_ = d.Ack(false)
	default:
		c.log.Warn("handling failed, requeueing", "err", err)
		_ = d.Nack(false, true)
	}
}
