// Package mongo stores queued jobs in MongoDB.
//
// It exists for the application that already runs MongoDB and has no SQL
// database for the database driver to use. The guarantees are the ones the
// other drivers give: a due job goes to exactly one worker, a job abandoned by
// a worker that died is handed out again once its reservation lapses, and a job
// that exhausts its attempts is kept rather than discarded.
//
// Durability is MongoDB's durability. That is a better trade than Redis's for
// most deployments — the jobs are on disk in the same cluster as the data they
// act on — but it is still worth knowing which database is the one that must
// not lose the job.
package mongo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	driver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	rylamongo "github.com/Dshonored/ryla/mongo"
	"github.com/Dshonored/ryla/queue"
)

// Collection names, mirroring the tables the database driver uses.
//
// They are constants rather than fields on Driver because the indexes below are
// declared from init, long before any Driver exists to be asked where it keeps
// things.
const (
	// JobsCollection holds pending and reserved jobs.
	JobsCollection = "jobs"
	// FailedCollection holds jobs that exhausted their attempts.
	FailedCollection = "failed_jobs"
	// countersCollection holds the id sequence. See nextID.
	countersCollection = "job_counters"
	// jobsCounter is the _id of the sequence document for JobsCollection.
	jobsCounter = "jobs"
)

func init() {
	rylamongo.Register(
		// The claim query in one index, in equality-sort-range order: the queue
		// is matched exactly, available_at is both the sort and a range bound,
		// and reserved_at narrows what is left. Without it every Claim is a
		// collection scan, which is fine on a dozen jobs and ruinous on a
		// million.
		rylamongo.Index{
			Collection: JobsCollection,
			Keys: bson.D{
				{Key: "queue", Value: 1},
				{Key: "available_at", Value: 1},
				{Key: "reserved_at", Value: 1},
			},
			Name: "jobs_claim",
		},
		// Failures are read newest first by a person looking at what broke.
		rylamongo.Index{
			Collection: FailedCollection,
			Keys:       bson.D{{Key: "failed_at", Value: -1}},
		},
		rylamongo.Index{
			Collection: FailedCollection,
			Keys:       bson.D{{Key: "queue", Value: 1}},
		},
	)
}

// Driver implements queue.Driver on top of MongoDB.
type Driver struct {
	DB *rylamongo.Database
	// Timeout is how long a claimed job may run before another worker may take
	// it. A worker killed mid-job would otherwise hold it forever.
	Timeout time.Duration
}

// New builds a MongoDB queue driver.
func New(db *rylamongo.Database) *Driver {
	return &Driver{DB: db, Timeout: queue.ReservationTimeout}
}

func (d *Driver) timeout() time.Duration {
	if d.Timeout > 0 {
		return d.Timeout
	}
	return queue.ReservationTimeout
}

func (d *Driver) jobs() *driver.Collection     { return d.DB.Collection(JobsCollection) }
func (d *Driver) failed() *driver.Collection   { return d.DB.Collection(FailedCollection) }
func (d *Driver) counters() *driver.Collection { return d.DB.Collection(countersCollection) }

// jobDoc is a queued job as stored.
//
// The _id is a minted integer rather than an ObjectID. queue.Record.ID is a
// uint, and an ObjectID is twelve bytes: it does not fit, and truncating it to
// eight throws away the part that makes it unique. Carrying the ObjectID in a
// side field and handing the worker a different number would mean two
// identities for one job, and every Complete, Release and Fail translating
// between them.
//
// So this takes the Redis driver's approach — mint the id ourselves — and that
// approach does hold here, because MongoDB has the same primitive Redis's INCR
// gave it: findAndModify with $inc is atomic, which is the long-standing way to
// get a sequence out of MongoDB. The one thing it costs is a round trip per
// Push. That is worth it for a queue, where the write is already durable and
// the read path is what matters, and it buys a numeric _id that survives the
// trip through queue.Record and back unchanged.
//
// MongoDB stores dates to the millisecond in UTC, so times read back are
// rounded and in UTC. Nothing here depends on finer resolution.
type jobDoc struct {
	ID          int64      `bson:"_id"`
	Queue       string     `bson:"queue"`
	Name        string     `bson:"name"`
	Payload     string     `bson:"payload"`
	Attempts    int        `bson:"attempts"`
	MaxAttempts int        `bson:"max_attempts"`
	AvailableAt time.Time  `bson:"available_at"`
	ReservedAt  *time.Time `bson:"reserved_at"`
	LastError   string     `bson:"last_error,omitempty"`
	CreatedAt   time.Time  `bson:"created_at"`
}

// failedDoc is a job that gave up. It keeps the job's own id, so the id an
// operator reads out of a log line is the id they pass to Retry.
type failedDoc struct {
	ID        int64     `bson:"_id"`
	Queue     string    `bson:"queue"`
	Name      string    `bson:"name"`
	Payload   string    `bson:"payload"`
	Attempts  int       `bson:"attempts"`
	LastError string    `bson:"last_error,omitempty"`
	FailedAt  time.Time `bson:"failed_at"`
}

// record converts a stored job into the shape the worker expects.
func (doc jobDoc) record() *queue.Record {
	return &queue.Record{
		ID:          uint(doc.ID),
		Queue:       doc.Queue,
		Name:        doc.Name,
		Payload:     doc.Payload,
		Attempts:    doc.Attempts,
		MaxAttempts: doc.MaxAttempts,
		AvailableAt: doc.AvailableAt,
		ReservedAt:  doc.ReservedAt,
		LastError:   doc.LastError,
		CreatedAt:   doc.CreatedAt,
	}
}

// nextID mints the next job id.
//
// findAndModify is atomic, so concurrent pushers each get a distinct number
// rather than two jobs sharing an id and overwriting each other.
func (d *Driver) nextID(ctx context.Context) (int64, error) {
	var doc struct {
		Seq int64 `bson:"seq"`
	}

	err := d.counters().FindOneAndUpdate(ctx,
		bson.M{"_id": jobsCounter},
		bson.M{"$inc": bson.M{"seq": int64(1)}},
		options.FindOneAndUpdate().
			SetUpsert(true).
			SetReturnDocument(options.After),
	).Decode(&doc)
	if err != nil {
		return 0, fmt.Errorf("queue/mongo: mint id: %w", err)
	}
	return doc.Seq, nil
}

// Push implements queue.Driver.
func (d *Driver) Push(ctx context.Context, rec *queue.Record) error {
	id, err := d.nextID(ctx)
	if err != nil {
		return err
	}

	created := rec.CreatedAt
	if created.IsZero() {
		created = time.Now()
	}

	doc := jobDoc{
		ID:          id,
		Queue:       rec.Queue,
		Name:        rec.Name,
		Payload:     rec.Payload,
		Attempts:    rec.Attempts,
		MaxAttempts: rec.MaxAttempts,
		AvailableAt: rec.AvailableAt.UTC(),
		CreatedAt:   created.UTC(),
	}

	if _, err := d.jobs().InsertOne(ctx, doc); err != nil {
		return fmt.Errorf("queue/mongo: push %s: %w", rec.Name, err)
	}

	// The caller gets the id back, so a job pushed outside a dispatcher can
	// still be completed or failed by the same record.
	rec.ID = uint(id)
	return nil
}

// Claim implements queue.Driver.
//
// Selecting the job and reserving it are one FindOneAndUpdate, which MongoDB
// applies atomically to the document it matched. Two workers racing therefore
// produce one winner and one empty result: whichever loses the race no longer
// matches the filter, because reserved_at is set by the time it looks, and it
// moves on to the next job or to nothing. There is no window between the read
// and the write for a second worker to slip into, which is the whole reason
// this is not a find followed by an update.
//
// A reservation older than the timeout is treated as absent, so a job held by a
// worker that was killed is handed out again rather than stuck forever.
func (d *Driver) Claim(ctx context.Context, q string) (*queue.Record, error) {
	now := time.Now().UTC()
	staleBefore := now.Add(-d.timeout())

	filter := bson.M{
		"queue":        q,
		"available_at": bson.M{"$lte": now},
		"$or": []bson.M{
			// A null match also catches a document with no reserved_at at all.
			{"reserved_at": nil},
			{"reserved_at": bson.M{"$lt": staleBefore}},
		},
	}

	update := bson.M{
		"$set": bson.M{"reserved_at": now},
		"$inc": bson.M{"attempts": 1},
	}

	opts := options.FindOneAndUpdate().
		// Oldest due job first, ties broken by id so the order is FIFO rather
		// than whatever the storage engine happens to return.
		SetSort(bson.D{{Key: "available_at", Value: 1}, {Key: "_id", Value: 1}}).
		SetReturnDocument(options.After)

	var doc jobDoc
	err := d.jobs().FindOneAndUpdate(ctx, filter, update, opts).Decode(&doc)
	if errors.Is(err, driver.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("queue/mongo: claim on %s: %w", q, err)
	}
	return doc.record(), nil
}

// Complete implements queue.Driver.
func (d *Driver) Complete(ctx context.Context, rec *queue.Record) error {
	if _, err := d.jobs().DeleteOne(ctx, bson.M{"_id": int64(rec.ID)}); err != nil {
		return fmt.Errorf("queue/mongo: complete %d: %w", rec.ID, err)
	}
	return nil
}

// Release implements queue.Driver.
func (d *Driver) Release(ctx context.Context, rec *queue.Record, at time.Time, reason string) error {
	update := bson.M{"$set": bson.M{
		"reserved_at":  nil,
		"available_at": at.UTC(),
		"last_error":   truncate(reason, 4000),
	}}

	if _, err := d.jobs().UpdateOne(ctx, bson.M{"_id": int64(rec.ID)}, update); err != nil {
		return fmt.Errorf("queue/mongo: release %d: %w", rec.ID, err)
	}
	return nil
}

// Fail implements queue.Driver.
//
// The move is two writes rather than a transaction: MongoDB only offers those
// on a replica set, and a queue that refuses to run on a standalone mongod
// would be a poor trade for a guarantee this can do without. The failure is
// recorded before the job is deleted, so a crash between the two leaves the job
// listed as failed and still pending — visible and re-runnable — rather than
// gone. The upsert is what makes that safe to repeat: a second Fail on the same
// job replaces the record instead of colliding on its id.
func (d *Driver) Fail(ctx context.Context, rec *queue.Record, reason string) error {
	doc := failedDoc{
		ID:        int64(rec.ID),
		Queue:     rec.Queue,
		Name:      rec.Name,
		Payload:   rec.Payload,
		Attempts:  rec.Attempts,
		LastError: truncate(reason, 4000),
		FailedAt:  time.Now().UTC(),
	}

	_, err := d.failed().ReplaceOne(ctx,
		bson.M{"_id": doc.ID}, doc, options.Replace().SetUpsert(true))
	if err != nil {
		return fmt.Errorf("queue/mongo: record failure of %d: %w", rec.ID, err)
	}

	if _, err := d.jobs().DeleteOne(ctx, bson.M{"_id": doc.ID}); err != nil {
		return fmt.Errorf("queue/mongo: remove failed job %d: %w", rec.ID, err)
	}
	return nil
}

// Pending implements queue.Driver.
func (d *Driver) Pending(ctx context.Context, q string) (int64, error) {
	n, err := d.jobs().CountDocuments(ctx, bson.M{"queue": q})
	if err != nil {
		return 0, fmt.Errorf("queue/mongo: count %s: %w", q, err)
	}
	return n, nil
}

// Failed returns jobs that exhausted their attempts, newest first.
func (d *Driver) Failed(ctx context.Context, limit int) ([]queue.FailedRecord, error) {
	if limit <= 0 {
		limit = 50
	}

	opts := options.Find().
		SetSort(bson.D{{Key: "failed_at", Value: -1}}).
		SetLimit(int64(limit))

	cursor, err := d.failed().Find(ctx, bson.M{}, opts)
	if err != nil {
		return nil, fmt.Errorf("queue/mongo: list failed jobs: %w", err)
	}
	defer cursor.Close(ctx)

	var docs []failedDoc
	if err := cursor.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("queue/mongo: decode failed jobs: %w", err)
	}

	out := make([]queue.FailedRecord, 0, len(docs))
	for _, doc := range docs {
		out = append(out, queue.FailedRecord{
			ID:        uint(doc.ID),
			Queue:     doc.Queue,
			Name:      doc.Name,
			Payload:   doc.Payload,
			Attempts:  doc.Attempts,
			LastError: doc.LastError,
			FailedAt:  doc.FailedAt,
		})
	}
	return out, nil
}

// Retry moves a failed job back onto its queue, with its attempt count reset.
//
// The job is queued before the failure record goes, for the same reason Fail
// writes in the order it does: an interruption should leave the operator
// looking at a job listed twice, not at one that has vanished from both places.
func (d *Driver) Retry(ctx context.Context, id uint) error {
	var doc failedDoc

	err := d.failed().FindOne(ctx, bson.M{"_id": int64(id)}).Decode(&doc)
	if errors.Is(err, driver.ErrNoDocuments) {
		return fmt.Errorf("queue/mongo: no failed job with id %d", id)
	}
	if err != nil {
		return fmt.Errorf("queue/mongo: read failed job %d: %w", id, err)
	}

	now := time.Now()
	rec := &queue.Record{
		Queue:       doc.Queue,
		Name:        doc.Name,
		Payload:     doc.Payload,
		MaxAttempts: queue.DefaultMaxAttempts,
		AvailableAt: now,
		CreatedAt:   now,
	}
	if err := d.Push(ctx, rec); err != nil {
		return err
	}

	if _, err := d.failed().DeleteOne(ctx, bson.M{"_id": doc.ID}); err != nil {
		return fmt.Errorf("queue/mongo: clear failed job %d: %w", id, err)
	}
	return nil
}

var (
	_ queue.Driver       = (*Driver)(nil)
	_ queue.FailedLister = (*Driver)(nil)
	_ queue.Retrier      = (*Driver)(nil)
)

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
