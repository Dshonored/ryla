package mail

import (
	"context"
	"errors"
	"time"

	"github.com/Dshonored/ryla/queue"
)

// JobName identifies the queued send job.
const JobName = "ryla.send-mail"

// sender is set by RegisterJob. The queue rebuilds a job from JSON and has no
// way to hand it a mailer, so the mailer has to be reachable from the job
// itself.
var sender Mailer

// SendJob delivers one message from a worker.
//
// Sending inside the request that triggered it makes the response as slow as
// the mail server and fails the request when the mail server is down. Neither
// is the user's problem, so the send moves onto a queue.
type SendJob struct {
	Message Message `json:"message"`
}

// Name implements queue.Job.
func (SendJob) Name() string { return JobName }

// Queue puts mail on its own queue, so a slow mail server cannot hold up
// everything else waiting to run.
func (SendJob) Queue() string { return "mail" }

// MaxAttempts implements queue.Retryable. Mail servers fail temporarily far
// more often than permanently, so this retries more than the default.
func (SendJob) MaxAttempts() int { return 5 }

// Backoff spreads retries over roughly half an hour.
func (SendJob) Backoff(attempt int) time.Duration {
	return time.Duration(attempt*attempt) * 30 * time.Second
}

// Run implements queue.Job.
func (j *SendJob) Run(ctx context.Context) error {
	if sender == nil {
		return errors.New("mail: no mailer registered; call mail.RegisterJob during start-up")
	}
	return sender.Send(ctx, j.Message)
}

// RegisterJob makes queued sending available, using m to deliver.
//
// Call it once during start-up, from the process that runs the worker as well
// as the one that queues the mail.
func RegisterJob(m Mailer) {
	sender = m
	queue.Register(func() queue.Job { return &SendJob{} })
}

// Queued wraps a mailer so Send enqueues instead of delivering.
//
// It is the mailer an application normally holds: handlers call Send and return
// immediately, and a worker does the waiting.
type Queued struct {
	Dispatcher *queue.Dispatcher
}

// NewQueued builds a queueing mailer.
func NewQueued(d *queue.Dispatcher) *Queued { return &Queued{Dispatcher: d} }

// Send implements Mailer by queueing the message.
//
// The message is validated now rather than in the worker: a malformed address
// discovered five retries later, in a log nobody is reading, is much worse than
// an error returned to the code that built it.
func (q *Queued) Send(ctx context.Context, msg Message) error {
	if err := msg.Validate(); err != nil {
		return err
	}
	return q.Dispatcher.Dispatch(ctx, &SendJob{Message: msg})
}
