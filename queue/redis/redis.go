// Package redis stores queued jobs in Redis.
//
// It handles far more throughput than the database driver and keeps queue
// traffic off the primary database, at the cost of durability being Redis's
// durability rather than your database's. For most work — email, webhooks,
// thumbnails — that trade is the right one; for jobs that must not be lost even
// if Redis does, keep using the database driver.
package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/Dshonored/ryla/queue"
)

// Driver implements queue.Driver on top of Redis.
type Driver struct {
	Client *goredis.Client
	Prefix string
	// Timeout is how long a claimed job may run before another worker may take
	// it. A worker killed mid-job would otherwise hold it forever.
	Timeout time.Duration
}

// New builds a Redis queue driver.
func New(client *goredis.Client, prefix string) *Driver {
	if prefix == "" {
		prefix = "queue"
	}
	return &Driver{Client: client, Prefix: prefix, Timeout: queue.ReservationTimeout}
}

func (d *Driver) timeout() time.Duration {
	if d.Timeout > 0 {
		return d.Timeout
	}
	return queue.ReservationTimeout
}

// Key layout, one set of keys per queue:
//
//	pending   sorted set of job ids, scored by the time they become due
//	reserved  sorted set of claimed ids, scored by when the claim expires
//	jobs      hash of id -> encoded record
//	attempts  hash of id -> attempt count
//	failed    list of encoded records that ran out of attempts
//	seq       counter used to mint ids
func (d *Driver) pendingKey(q string) string  { return d.Prefix + ":" + q + ":pending" }
func (d *Driver) reservedKey(q string) string { return d.Prefix + ":" + q + ":reserved" }
func (d *Driver) jobsKey(q string) string     { return d.Prefix + ":" + q + ":jobs" }
func (d *Driver) attemptsKey(q string) string { return d.Prefix + ":" + q + ":attempts" }
func (d *Driver) failedKey() string           { return d.Prefix + ":failed" }
func (d *Driver) seqKey() string              { return d.Prefix + ":seq" }

// claimScript reserves the next due job.
//
// It runs as one Lua script because the whole point is atomicity: reclaiming
// stale reservations, picking the next due id, moving it, and bumping its
// attempt count have to happen together, or two workers can hold the same job.
// Redis executes a script without interleaving anything else.
var claimScript = goredis.NewScript(`
local pending  = KEYS[1]
local reserved = KEYS[2]
local jobs     = KEYS[3]
local attempts = KEYS[4]
local now      = tonumber(ARGV[1])
local expires  = tonumber(ARGV[2])

-- Hand back anything whose reservation has lapsed, so a job abandoned by a
-- dead worker is picked up rather than lost.
local stale = redis.call('ZRANGEBYSCORE', reserved, '-inf', now)
for i = 1, #stale do
  redis.call('ZREM', reserved, stale[i])
  redis.call('ZADD', pending, now, stale[i])
end

local due = redis.call('ZRANGEBYSCORE', pending, '-inf', now, 'LIMIT', 0, 1)
if #due == 0 then
  return nil
end

local id = due[1]
redis.call('ZREM', pending, id)
redis.call('ZADD', reserved, expires, id)

local record = redis.call('HGET', jobs, id)
if not record then
  -- The record vanished; drop the reservation rather than handing back a
  -- job with no payload.
  redis.call('ZREM', reserved, id)
  return nil
end

local count = redis.call('HINCRBY', attempts, id, 1)
return {id, record, count}
`)

// stored is the record as kept in the jobs hash. queue.Record carries a
// database primary key; here the id is a string, so the two are converted at
// the boundary.
type stored struct {
	ID          string    `json:"id"`
	Queue       string    `json:"queue"`
	Name        string    `json:"name"`
	Payload     string    `json:"payload"`
	MaxAttempts int       `json:"max_attempts"`
	AvailableAt time.Time `json:"available_at"`
	CreatedAt   time.Time `json:"created_at"`
	LastError   string    `json:"last_error,omitempty"`
	Attempts    int       `json:"attempts,omitempty"`
}

// Push implements queue.Driver.
func (d *Driver) Push(ctx context.Context, rec *queue.Record) error {
	id, err := d.Client.Incr(ctx, d.seqKey()).Result()
	if err != nil {
		return fmt.Errorf("queue/redis: mint id: %w", err)
	}
	jobID := strconv.FormatInt(id, 10)

	raw, err := json.Marshal(stored{
		ID:          jobID,
		Queue:       rec.Queue,
		Name:        rec.Name,
		Payload:     rec.Payload,
		MaxAttempts: rec.MaxAttempts,
		AvailableAt: rec.AvailableAt,
		CreatedAt:   rec.CreatedAt,
	})
	if err != nil {
		return err
	}

	// One round trip, and the job is never visible in pending before its
	// payload exists in the hash.
	pipe := d.Client.TxPipeline()
	pipe.HSet(ctx, d.jobsKey(rec.Queue), jobID, raw)
	pipe.ZAdd(ctx, d.pendingKey(rec.Queue), goredis.Z{
		Score:  float64(rec.AvailableAt.UnixMilli()),
		Member: jobID,
	})
	_, err = pipe.Exec(ctx)
	return err
}

// Claim implements queue.Driver.
func (d *Driver) Claim(ctx context.Context, q string) (*queue.Record, error) {
	now := time.Now()

	result, err := claimScript.Run(ctx, d.Client,
		[]string{d.pendingKey(q), d.reservedKey(q), d.jobsKey(q), d.attemptsKey(q)},
		now.UnixMilli(),
		now.Add(d.timeout()).UnixMilli(),
	).Result()

	if errors.Is(err, goredis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	values, ok := result.([]any)
	if !ok || len(values) != 3 {
		return nil, fmt.Errorf("queue/redis: unexpected claim result %T", result)
	}

	raw, _ := values[1].(string)

	var s stored
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return nil, fmt.Errorf("queue/redis: decode record: %w", err)
	}

	attempts, _ := values[2].(int64)
	return d.toRecord(s, int(attempts)), nil
}

// toRecord converts a stored job into the shape the worker expects.
//
// queue.Record's ID is numeric, so the string id is carried in LastError-free
// form via the queue-scoped hash instead; parsing it back keeps Complete,
// Release and Fail able to find the job again.
func (d *Driver) toRecord(s stored, attempts int) *queue.Record {
	id, _ := strconv.ParseUint(s.ID, 10, 64)
	return &queue.Record{
		ID:          uint(id),
		Queue:       s.Queue,
		Name:        s.Name,
		Payload:     s.Payload,
		Attempts:    attempts,
		MaxAttempts: s.MaxAttempts,
		AvailableAt: s.AvailableAt,
		CreatedAt:   s.CreatedAt,
		LastError:   s.LastError,
	}
}

func jobID(rec *queue.Record) string { return strconv.FormatUint(uint64(rec.ID), 10) }

// Complete implements queue.Driver.
func (d *Driver) Complete(ctx context.Context, rec *queue.Record) error {
	id := jobID(rec)

	pipe := d.Client.TxPipeline()
	pipe.ZRem(ctx, d.reservedKey(rec.Queue), id)
	pipe.HDel(ctx, d.jobsKey(rec.Queue), id)
	pipe.HDel(ctx, d.attemptsKey(rec.Queue), id)
	_, err := pipe.Exec(ctx)
	return err
}

// Release implements queue.Driver.
func (d *Driver) Release(ctx context.Context, rec *queue.Record, at time.Time, reason string) error {
	id := jobID(rec)

	raw, err := json.Marshal(stored{
		ID:          id,
		Queue:       rec.Queue,
		Name:        rec.Name,
		Payload:     rec.Payload,
		MaxAttempts: rec.MaxAttempts,
		AvailableAt: at,
		CreatedAt:   rec.CreatedAt,
		LastError:   truncate(reason, 4000),
	})
	if err != nil {
		return err
	}

	pipe := d.Client.TxPipeline()
	pipe.HSet(ctx, d.jobsKey(rec.Queue), id, raw)
	pipe.ZRem(ctx, d.reservedKey(rec.Queue), id)
	pipe.ZAdd(ctx, d.pendingKey(rec.Queue), goredis.Z{
		Score:  float64(at.UnixMilli()),
		Member: id,
	})
	_, err = pipe.Exec(ctx)
	return err
}

// Fail implements queue.Driver.
func (d *Driver) Fail(ctx context.Context, rec *queue.Record, reason string) error {
	id := jobID(rec)

	raw, err := json.Marshal(stored{
		ID:          id,
		Queue:       rec.Queue,
		Name:        rec.Name,
		Payload:     rec.Payload,
		MaxAttempts: rec.MaxAttempts,
		Attempts:    rec.Attempts,
		CreatedAt:   rec.CreatedAt,
		LastError:   truncate(reason, 4000),
	})
	if err != nil {
		return err
	}

	pipe := d.Client.TxPipeline()
	// Kept rather than discarded: a job that gave up is usually the first sign
	// of a broken dependency.
	pipe.LPush(ctx, d.failedKey(), raw)
	pipe.LTrim(ctx, d.failedKey(), 0, 999)
	pipe.ZRem(ctx, d.reservedKey(rec.Queue), id)
	pipe.HDel(ctx, d.jobsKey(rec.Queue), id)
	pipe.HDel(ctx, d.attemptsKey(rec.Queue), id)
	_, err = pipe.Exec(ctx)
	return err
}

// Pending implements queue.Driver.
func (d *Driver) Pending(ctx context.Context, q string) (int64, error) {
	return d.Client.ZCard(ctx, d.pendingKey(q)).Result()
}

// Failed returns jobs that ran out of attempts, newest first.
func (d *Driver) Failed(ctx context.Context, limit int) ([]queue.FailedRecord, error) {
	if limit <= 0 {
		limit = 50
	}

	raws, err := d.Client.LRange(ctx, d.failedKey(), 0, int64(limit-1)).Result()
	if err != nil {
		return nil, err
	}

	out := make([]queue.FailedRecord, 0, len(raws))
	for _, raw := range raws {
		var s stored
		if err := json.Unmarshal([]byte(raw), &s); err != nil {
			continue
		}
		id, _ := strconv.ParseUint(s.ID, 10, 64)
		out = append(out, queue.FailedRecord{
			ID:        uint(id),
			Queue:     s.Queue,
			Name:      s.Name,
			Payload:   s.Payload,
			Attempts:  s.Attempts,
			LastError: s.LastError,
			FailedAt:  s.CreatedAt,
		})
	}
	return out, nil
}

var _ queue.Driver = (*Driver)(nil)

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

// Retry moves a failed job back onto its queue with its attempts reset.
//
// The failed list is scanned rather than indexed: it is capped at a thousand
// entries and only ever read by a person looking at what broke, so a linear
// walk costs nothing and avoids a second structure to keep in step.
func (d *Driver) Retry(ctx context.Context, id uint) error {
	want := strconv.FormatUint(uint64(id), 10)

	raws, err := d.Client.LRange(ctx, d.failedKey(), 0, -1).Result()
	if err != nil {
		return err
	}

	for _, raw := range raws {
		var s stored
		if err := json.Unmarshal([]byte(raw), &s); err != nil {
			continue
		}
		if s.ID != want {
			continue
		}

		rec := &queue.Record{
			Queue:       s.Queue,
			Name:        s.Name,
			Payload:     s.Payload,
			MaxAttempts: s.MaxAttempts,
			AvailableAt: time.Now(),
			CreatedAt:   time.Now(),
		}
		if err := d.Push(ctx, rec); err != nil {
			return err
		}
		return d.Client.LRem(ctx, d.failedKey(), 1, raw).Err()
	}
	return fmt.Errorf("queue/redis: no failed job with id %d", id)
}
