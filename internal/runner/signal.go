package runner

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// WithSignalHandling wraps the given context so that SIGINT or SIGTERM
// triggers context cancellation. The returned cancel function restores
// default signal handling and should be called when done.
//
// Usage:
//
//	ctx, cancel := WithSignalHandling(ctx)
//	defer cancel()
//	result, err := Run(ctx, cfg)
func WithSignalHandling(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
		}
		signal.Stop(sigCh)
	}()

	return ctx, cancel
}
