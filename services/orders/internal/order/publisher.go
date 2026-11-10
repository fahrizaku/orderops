package order

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Publisher sends order events to the message broker.
type Publisher interface {
	Publish(ctx context.Context, ev OrderCreatedEvent) error
}

// AMQPPublisher publishes to the topic exchange "orders" with routing key
// "order.created". It connects lazily and drops a broken connection so the
// next call redials — the outbox relay simply retries in the meantime.
type AMQPPublisher struct {
	url  string
	mu   sync.Mutex
	conn *amqp.Connection
	ch   *amqp.Channel
}

func NewAMQPPublisher(url string) *AMQPPublisher {
	return &AMQPPublisher{url: url}
}

func (p *AMQPPublisher) Publish(ctx context.Context, ev OrderCreatedEvent) error {
	body, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.ensureChannel(); err != nil {
		return err
	}
	err = p.ch.PublishWithContext(ctx, "orders", "order.created", false, false, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		Timestamp:    time.Now(),
		Body:         body,
	})
	if err != nil {
		p.reset()
		return err
	}
	return nil
}

func (p *AMQPPublisher) ensureChannel() error {
	if p.conn != nil && !p.conn.IsClosed() && p.ch != nil && !p.ch.IsClosed() {
		return nil
	}
	p.reset()
	conn, err := amqp.Dial(p.url)
	if err != nil {
		return err
	}
	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return err
	}
	if err := ch.ExchangeDeclare("orders", "topic", true, false, false, false, nil); err != nil {
		_ = conn.Close()
		return err
	}
	p.conn, p.ch = conn, ch
	return nil
}

func (p *AMQPPublisher) reset() {
	if p.ch != nil {
		_ = p.ch.Close()
		p.ch = nil
	}
	if p.conn != nil {
		_ = p.conn.Close()
		p.conn = nil
	}
}

func (p *AMQPPublisher) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reset()
}
