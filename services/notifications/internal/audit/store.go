package audit

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
)

// Store persists audit entries. Save is idempotent on EventID: writing
// the same event twice reports inserted=false and no error, so consumers
// can safely acknowledge redeliveries.
type Store interface {
	Save(ctx context.Context, e Entry) (inserted bool, err error)
	List(ctx context.Context, limit, offset int64) ([]Entry, error)
	Ping(ctx context.Context) error
}

// MongoStore keeps the audit log in MongoDB, one document per event.
type MongoStore struct {
	client *mongo.Client
	coll   *mongo.Collection
}

func NewMongoStore(client *mongo.Client, db string) *MongoStore {
	return &MongoStore{
		client: client,
		coll:   client.Database(db).Collection("audit_events"),
	}
}

// EnsureIndexes creates the unique event_id index (the idempotency key)
// and a received_at index backing the list endpoint's sort.
func (s *MongoStore) EnsureIndexes(ctx context.Context) error {
	_, err := s.coll.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "event_id", Value: 1}},
			Options: options.Index().SetUnique(true),
		},
		{
			Keys: bson.D{{Key: "received_at", Value: -1}},
		},
	})
	return err
}

func (s *MongoStore) Save(ctx context.Context, e Entry) (bool, error) {
	_, err := s.coll.InsertOne(ctx, e)
	if mongo.IsDuplicateKeyError(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *MongoStore) List(ctx context.Context, limit, offset int64) ([]Entry, error) {
	opts := options.Find().
		SetSort(bson.D{{Key: "received_at", Value: -1}}).
		SetLimit(limit).
		SetSkip(offset)
	cur, err := s.coll.Find(ctx, bson.D{}, opts)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	entries := []Entry{}
	for cur.Next(ctx) {
		var e Entry
		if err := cur.Decode(&e); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, cur.Err()
}

func (s *MongoStore) Ping(ctx context.Context) error {
	return s.client.Ping(ctx, readpref.Primary())
}
