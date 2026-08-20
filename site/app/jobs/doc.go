// Package jobs holds this application's background work.
//
// A job is an ordinary struct. Its exported fields are the payload, serialised
// when it is dispatched and restored before it runs:
//
//	type SendWelcomeEmail struct {
//	    UserID uint `json:"user_id"`
//	}
//
//	func (SendWelcomeEmail) Name() string { return "send-welcome-email" }
//
//	func (j SendWelcomeEmail) Run(ctx context.Context) error { ... }
//
// Keep payloads small — store an id, not a whole record. By the time the job
// runs the row may have changed, and reading it fresh is almost always what you
// want.
//
// Jobs must tolerate running twice. A worker killed after finishing the work
// but before deleting the row will run it again, and no queue can promise
// otherwise without the job's help.
//
// Register from init so importing this package is enough:
//
//	func init() {
//	    queue.Register(func() queue.Job { return &SendWelcomeEmail{} })
//	}
//
// Create one with:
//
//	ry make:job SendWelcomeEmail
package jobs
