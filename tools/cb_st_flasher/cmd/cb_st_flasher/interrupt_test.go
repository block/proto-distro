//go:build darwin || linux

package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestInterruptProcessHelper(t *testing.T) {
	mode := os.Getenv("CB_ST_FLASHER_INTERRUPT_TEST")
	if mode == "" {
		return
	}
	if mode == "help" || mode == "invalid-option" {
		arg := "--help"
		if mode == "invalid-option" {
			arg = "--not-a-flasher-option"
		}
		os.Args = []string{"cb_st_flasher", arg}
		main()
		return
	}
	ctx, stop := interruptContext()
	defer stop()
	gate := func() {
		var b [1]byte
		if _, err := io.ReadFull(os.Stdin, b[:]); err != nil {
			panic(err)
		}
	}
	fmt.Println("READY")
	work := func() error {
		defer func() {
			fmt.Println("CLEANUP_STARTED")
			if mode == "blocked-cleanup" {
				select {}
			}
			gate()
			fmt.Println("CLEANUP_FINISHED")
		}()
		if mode == "blocked-work" {
			go func() {
				<-ctx.Done()
				fmt.Println("CANCELLED")
			}()
			select {}
		}
		<-ctx.Done()
		return ctx.Err()
	}
	err := work()
	stop()
	if mode == "disarm" {
		stop()
		fmt.Println("STOPPED")
		gate()
		return
	}
	if err != nil {
		logError("%v", err)
		os.Exit(1)
	}
}

type interruptChild struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	markers  chan string
	done     chan struct{}
	readDone chan struct{}
	waitErr  error
	readErr  error
	stderr   bytes.Buffer
	stdout   bytes.Buffer
}

func startInterruptChild(t *testing.T, mode string, blockedStderr bool) *interruptChild {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c := &interruptChild{
		cmd:     exec.Command(executable, "-test.run=^TestInterruptProcessHelper$"),
		markers: make(chan string, 128), done: make(chan struct{}), readDone: make(chan struct{}),
	}
	// Race instrumentation must not add its default one-second process-exit sleep.
	c.cmd.Env = append(os.Environ(), "CB_ST_FLASHER_INTERRUPT_TEST="+mode,
		"GORACE="+os.Getenv("GORACE")+" atexit_sleep_ms=0")
	c.stdin, err = c.cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.stdin.Close() })
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stdoutR.Close(); stdoutW.Close() })
	c.cmd.Stdout = stdoutW
	c.cmd.Stderr = &c.stderr // exec continuously drains diagnostics; Wait joins that copy.
	if blockedStderr {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { r.Close(); w.Close() })
		fd := int(w.Fd())
		if err := syscall.SetNonblock(fd, true); err != nil {
			t.Fatal(err)
		}
		// Single-byte writes fill even the final partial pipe buffer.
		for {
			_, err := syscall.Write(fd, []byte{'x'})
			if err == syscall.EAGAIN {
				break
			}
			if err == syscall.EINTR {
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := syscall.SetNonblock(fd, false); err != nil {
			t.Fatal(err)
		}
		c.cmd.Stderr = w // Keep r open and deliberately undrained until teardown.
	}
	if err := c.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	stdoutW.Close()
	go func() {
		defer close(c.readDone)
		defer close(c.markers)
		scanner := bufio.NewScanner(stdoutR)
		for scanner.Scan() {
			line := scanner.Text()
			fmt.Fprintln(&c.stdout, line)
			switch line {
			case "READY", "CANCELLED", "CLEANUP_STARTED", "CLEANUP_FINISHED", "STOPPED":
				c.markers <- line
			}
		}
		c.readErr = scanner.Err()
	}()
	go func() {
		c.waitErr = c.cmd.Wait() // Sole owner of Wait, including failed-test cleanup.
		close(c.done)
	}()
	t.Cleanup(func() {
		select {
		case <-c.done:
		default:
			c.cmd.Process.Kill()
			select {
			case <-c.done:
			case <-time.After(5 * time.Second):
				t.Error("failed to reap interrupt helper after kill")
			}
		}
	})
	return c
}

func (c *interruptChild) marker(t *testing.T, want string) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case got, ok := <-c.markers:
			if !ok {
				t.Fatalf("helper closed stdout before %s", want)
			}
			if got == want {
				return
			}
		case <-timer.C:
			t.Fatalf("helper never emitted %s", want)
		}
	}
}

func (c *interruptChild) signal(t *testing.T, sig os.Signal) {
	t.Helper()
	if err := c.cmd.Process.Signal(sig); err != nil {
		t.Fatal(err)
	}
}

func (c *interruptChild) release(t *testing.T) {
	t.Helper()
	if _, err := io.WriteString(c.stdin, "x"); err != nil {
		t.Fatal(err)
	}
}

func (c *interruptChild) exit(t *testing.T, code int, timeout time.Duration) {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-c.done:
	case <-timer.C:
		t.Fatal("helper survived exit deadline; watchdog kill is not successful termination")
	}
	status, ok := c.cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !ok || !status.Exited() || status.ExitStatus() != code {
		t.Fatalf("want ordinary exit %d, got %v (%v); stderr: %s", code, status, c.waitErr, c.stderr.String())
	}
	select {
	case <-c.readDone:
		if c.readErr != nil {
			t.Fatal(c.readErr)
		}
	case <-timer.C:
		t.Fatal("stdout did not close after helper exit")
	}
}

func TestInterruptLifecycle(t *testing.T) {
	for _, sig := range []os.Signal{syscall.SIGINT, syscall.SIGTERM} {
		t.Run("graceful/"+sig.String(), func(t *testing.T) {
			c := startInterruptChild(t, "graceful", false)
			c.marker(t, "READY")
			start := time.Now()
			c.signal(t, sig)
			c.marker(t, "CLEANUP_STARTED")
			if time.Since(start) >= time.Second {
				t.Fatal("cleanup acknowledgement left insufficient grace")
			}
			c.release(t)
			c.marker(t, "CLEANUP_FINISHED")
			c.exit(t, 1, time.Second)
		})
	}
	for _, tc := range []struct {
		name, mode, ack       string
		first                 os.Signal
		second, blockedStderr bool
	}{
		{"blocked-work", "blocked-work", "CANCELLED", syscall.SIGINT, false, false},
		{"blocked-cleanup", "blocked-cleanup", "CLEANUP_STARTED", syscall.SIGTERM, false, false},
		{"second-interrupt-work", "blocked-work", "CANCELLED", syscall.SIGINT, true, false},
		{"second-interrupt-cleanup", "blocked-cleanup", "CLEANUP_STARTED", syscall.SIGTERM, true, false},
		{"stderr-backpressure", "blocked-work", "CANCELLED", syscall.SIGINT, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := startInterruptChild(t, tc.mode, tc.blockedStderr)
			c.marker(t, "READY")
			start := time.Now()
			c.signal(t, tc.first)
			c.marker(t, tc.ack)
			if tc.second {
				if time.Since(start) >= time.Second {
					t.Fatal("cancellation acknowledgement too late to distinguish second signal from timer")
				}
				second := time.Now()
				c.signal(t, syscall.SIGINT)
				c.exit(t, 1, time.Second)
				if time.Since(second) >= time.Second || time.Since(start) >= 1800*time.Millisecond {
					t.Fatal("second signal did not exit before automatic expiry")
				}
			} else {
				c.exit(t, 1, max(time.Nanosecond, 5*time.Second-time.Since(start)))
				if elapsed := time.Since(start); elapsed < 1800*time.Millisecond || elapsed > 5*time.Second {
					t.Fatalf("automatic exit outside grace budget: %s", elapsed)
				}
			}
			output := c.stdout.String()
			if strings.Contains(output, "CLEANUP_FINISHED") || (tc.mode == "blocked-work" && strings.Contains(output, "CLEANUP_STARTED")) {
				t.Fatalf("blocked work/cleanup unexpectedly completed: %s", output)
			}
		})
	}
	t.Run("timer-disarm", func(t *testing.T) {
		c := startInterruptChild(t, "disarm", false)
		c.marker(t, "READY")
		c.signal(t, syscall.SIGINT)
		c.marker(t, "CLEANUP_STARTED")
		c.release(t)
		c.marker(t, "CLEANUP_FINISHED")
		c.marker(t, "STOPPED")
		select {
		case <-c.done:
			t.Fatalf("stopped watcher left a delayed exit armed: %v", c.waitErr)
		case <-time.After(2300 * time.Millisecond):
		}
		c.release(t)
		c.exit(t, 0, time.Second)
	})
	for _, mode := range []string{"help", "invalid-option"} {
		t.Run("main/"+mode, func(t *testing.T) {
			c := startInterruptChild(t, mode, false)
			code := 0
			if mode == "invalid-option" {
				code = 1
			}
			c.exit(t, code, 5*time.Second)
		})
	}
}
