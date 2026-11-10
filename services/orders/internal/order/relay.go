package order

import (
	"context"
	"log/slog"
	"time"
)

// Relay drains the transactional outbox to the broker. Events go out in
// insertion order; the first failure stops the round so a later event can
// never overtake an earlier one, and unsent rows are retried next round.
type Relay struct {
	repo     Repository
	pub      Publisher
	log      *slog.Logger
	interval time.Duration
}

func NewRelay(repo Repository, pub Publisher, log *slog.Logger, interval time.Duration) *Relay {
	return &Relay{repo: repo, pub: pub, log: log, interval: interval}
}

func (r *Relay) Run(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.Flush(ctx)
		}
	}
}

// Flush publishes one batch of pending outbox events.
func (r *Relay) Flush(ctx context.Context) {
	events, err := r.repo.PendingOutbox(ctx, 50)
	if err != nil {
		r.log.Warn("outbox read failed", "err", err)
		return
	}
	for _, ev := range events {
		if err := r.pub.Publish(ctx, ev.Payload); err != nil {
			r.log.Warn("outbox publish failed, will retry", "event_id", ev.ID, "err", err)
			return
		}
		if err := r.repo.MarkOutboxSent(ctx, ev.ID); err != nil {
			r.log.Warn("outbox mark-sent failed", "event_id", ev.ID, "err", err)
			return
		}
	}
}
