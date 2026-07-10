//go:build !windows && !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package main

import "context"

// Platforms without the Unix signal set or Windows console handling still get
// a cancellable execution context. Bounded lifetime remains enforced by the
// process runner's maximum duration.
func runExecutionContext() (context.Context, context.CancelFunc) {
	return context.WithCancel(context.Background())
}
