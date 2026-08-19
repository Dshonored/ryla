// Package app provides the runtime pieces every Ryla application binary needs:
// an HTTP server that shuts down gracefully, and the small command dispatcher
// that gives the built binary its `serve` / `migrate` / `queue:work` subcommands.
//
// The application's own App struct is generated into the project rather than
// defined here, so it can hold that project's concrete config type with no
// interfaces or reflection in the way.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// Server runs an http.Handler and shuts it down cleanly when its context is
// cancelled.
type Server struct {
	Addr    string
	Handler http.Handler
	Log     *slog.Logger

	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration

	// ShutdownTimeout bounds how long in-flight requests get to finish once
	// shutdown starts. Past it, the server closes connections and returns.
	ShutdownTimeout time.Duration
}

// Run starts the server and blocks until ctx is cancelled or the server fails.
// On cancellation it stops accepting connections and waits up to
// ShutdownTimeout for in-flight requests before returning.
func (s *Server) Run(ctx context.Context) error {
	log := s.Log
	if log == nil {
		log = slog.Default()
	}

	srv := &http.Server{
		Addr:              s.Addr,
		Handler:           s.Handler,
		ReadHeaderTimeout: orDefault(s.ReadHeaderTimeout, 10*time.Second),
		ReadTimeout:       orDefault(s.ReadTimeout, 30*time.Second),
		WriteTimeout:      orDefault(s.WriteTimeout, 30*time.Second),
		IdleTimeout:       orDefault(s.IdleTimeout, 120*time.Second),
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}

	// Bind before announcing. ListenAndServe would let the "listening" line be
	// logged a moment before the bind fails, which reads as a working server
	// immediately followed by an inexplicable error.
	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", srv.Addr, err)
	}

	log.Info("server listening", "addr", ln.Addr().String())

	errCh := make(chan error, 1)
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	log.Info("server shutting down")

	// A fresh context: ctx is already cancelled, and shutdown needs its own
	// deadline to drain in-flight requests.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), orDefault(s.ShutdownTimeout, 15*time.Second))
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Warn("graceful shutdown timed out, forcing close", "error", err)
		return srv.Close()
	}

	log.Info("server stopped")
	return nil
}

func orDefault(d, def time.Duration) time.Duration {
	if d <= 0 {
		return def
	}
	return d
}
