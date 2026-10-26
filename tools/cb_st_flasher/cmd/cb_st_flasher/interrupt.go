package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

const interruptGracePeriod = 2 * time.Second

func interruptContext() (context.Context, func()) {
	ctx, cancel := context.WithCancel(context.Background())
	signals := make(chan os.Signal, 2)
	stopped := make(chan struct{})
	finished := make(chan struct{})
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)

	go func() {
		defer close(finished)
		select {
		case <-stopped:
			return
		case <-signals:
		}

		timer := time.NewTimer(interruptGracePeriod)
		defer timer.Stop()
		cancel()
		// A blocked diagnostic must not prevent escalation.
		go func() {
			fmt.Fprintln(os.Stderr, "Stopping; press Ctrl-C again to force exit. Cleanup has 2s; restart ROM/stm32prog before retrying an interrupted flash.")
		}()

		// Cancellation does not disarm escalation: work and cleanup may both block.
		select {
		case <-stopped:
			return
		case <-signals:
		case <-timer.C:
		}
		os.Exit(1)
	}()

	return ctx, sync.OnceFunc(func() {
		signal.Stop(signals)
		close(stopped)
		<-finished
		cancel()
	})
}
