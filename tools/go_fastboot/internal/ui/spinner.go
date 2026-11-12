// This file implements interactive spinner and status line components for progress feedback.
package ui

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/schollz/progressbar/v3"
	"golang.org/x/term"
)

const (
	spinnerType        = 14
	spinnerThrottle    = 65 * time.Millisecond
	spinnerTickerDelay = 100 * time.Millisecond
	progressBarWidth   = 40
)

// isTerminal returns true if stderr is connected to a terminal.
// Used to disable interactive elements (spinners, progress bars) in CI/pipe environments.
func isTerminal() bool {
	return term.IsTerminal(int(os.Stderr.Fd()))
}

// Spinner wraps progressbar for spinner functionality
type Spinner struct {
	mu      sync.Mutex
	bar     *progressbar.ProgressBar
	message string
	done    chan struct{}
}

// NewSpinner creates a new spinner.
func NewSpinner() *Spinner {
	return &Spinner{}
}

// Start begins the spinner with a message
func (s *Spinner) Start(message string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Stop any existing spinner first to prevent goroutine leaks
	s.stopLocked()

	s.message = message

	// In non-terminal environments (CI, pipes), skip the animated spinner
	// to avoid spammy output. Just print the initial message once.
	if !isTerminal() {
		fmt.Fprintf(os.Stderr, "%s...\n", message)
		return
	}

	s.done = make(chan struct{})
	s.bar = progressbar.NewOptions(-1,
		progressbar.OptionSetDescription(message),
		progressbar.OptionSetWriter(os.Stderr),
		progressbar.OptionSpinnerType(spinnerType),
		progressbar.OptionSetRenderBlankState(true),
		progressbar.OptionThrottle(spinnerThrottle),
		progressbar.OptionClearOnFinish(),
	)

	// Capture references for the goroutine
	done := s.done
	bar := s.bar

	// Start spinning in background
	go func() {
		ticker := time.NewTicker(spinnerTickerDelay)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				bar.Add(1)
			}
		}
	}()
}

// Update changes the spinner message
func (s *Spinner) Update(message string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.message = message
	if s.bar != nil {
		s.bar.Describe(message)
	}
}

// stopLocked stops the spinner (must be called with mu held)
func (s *Spinner) stopLocked() {
	if s.done != nil {
		close(s.done)
		s.done = nil
	}
	if s.bar != nil {
		s.bar.Finish()
		s.bar = nil
	}
	// Only send escape sequences when output is a terminal
	// This prevents garbled output when redirected to files/pipes or in CI environments
	if term.IsTerminal(int(os.Stderr.Fd())) {
		fmt.Fprint(os.Stderr, "\r\033[K") // Clear line
	}
}

// Stop stops the spinner without printing a message
func (s *Spinner) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopLocked()
}

// Success stops the spinner with a success message
func (s *Spinner) Success(message string) {
	s.Stop()
	fmt.Fprintf(os.Stdout, "%s %s\n", SuccessIcon, message)
}

// Error stops the spinner with an error message
func (s *Spinner) Error(message string) {
	s.Stop()
	fmt.Fprintf(os.Stdout, "%s %s\n", ErrorIcon, message)
}

// Warning stops the spinner with a warning message
func (s *Spinner) Warning(message string) {
	s.Stop()
	fmt.Fprintf(os.Stdout, "%s %s\n", WarningIcon, message)
}

// ProgressBar wraps progressbar for progress functionality
type ProgressBar struct {
	bar            *progressbar.ProgressBar
	total          int
	message        string
	lastLogPercent int  // Track last logged percentage for CI mode
	isTerminal     bool // Cache terminal detection
}

// ciLogInterval defines the percentage interval for CI progress logging.
// Progress is logged at 0%, 25%, 50%, 75%, and 100%.
const ciLogInterval = 25

// NewProgressBar creates a new progress bar.
func NewProgressBar(total int) *ProgressBar {
	isTerm := isTerminal()
	p := &ProgressBar{
		total:          total,
		lastLogPercent: -1, // Ensure first update logs
		isTerminal:     isTerm,
	}

	// In non-terminal environments (CI, pipes), skip the animated progress bar.
	// Progress will be logged at intervals (0%, 25%, 50%, 75%, 100%).
	if !isTerm {
		return p
	}

	p.bar = progressbar.NewOptions(total,
		progressbar.OptionSetWriter(os.Stderr),
		progressbar.OptionEnableColorCodes(true),
		progressbar.OptionShowBytes(false),
		progressbar.OptionSetWidth(progressBarWidth),
		progressbar.OptionShowCount(),
		progressbar.OptionSetPredictTime(false), // Disable predicted remaining time
		progressbar.OptionClearOnFinish(),
	)
	return p
}

// SetMessage sets the progress message
func (p *ProgressBar) SetMessage(message string) {
	p.message = message
	if p.bar != nil {
		p.bar.Describe(message)
	}
}

// Set sets the current progress value
func (p *ProgressBar) Set(current int) {
	if p.bar != nil {
		p.bar.Set(current)
		return
	}

	// In CI mode, log progress at intervals to show the job is progressing
	if !p.isTerminal && p.total > 0 {
		percent := (current * 100) / p.total
		// Log at each interval crossing (0, 25, 50, 75, 100)
		logPercent := (percent / ciLogInterval) * ciLogInterval
		if logPercent > p.lastLogPercent {
			p.lastLogPercent = logPercent
			if p.message != "" {
				fmt.Fprintf(os.Stderr, "  Progress: %d%% - %s\n", percent, p.message)
			} else {
				fmt.Fprintf(os.Stderr, "  Progress: %d%%\n", percent)
			}
		}
	}
}

// Complete marks the progress as complete
func (p *ProgressBar) Complete(message string) {
	if p.bar != nil {
		p.bar.Finish()
	}
	fmt.Fprintf(os.Stdout, "\n%s %s\n", SuccessIcon, message)
}

// Error marks the progress as failed
func (p *ProgressBar) Error(message string) {
	if p.bar != nil {
		p.bar.Finish()
	}
	fmt.Fprintf(os.Stdout, "\n%s %s\n", ErrorIcon, message)
}

// StatusLine displays status messages
type StatusLine struct{}

// NewStatusLine creates a new status line.
func NewStatusLine() *StatusLine {
	return &StatusLine{}
}

// Info prints an info message
func (s *StatusLine) Info(message string) {
	fmt.Fprintf(os.Stdout, "%s %s\n", InfoIcon, message)
}

// Success prints a success message
func (s *StatusLine) Success(message string) {
	fmt.Fprintf(os.Stdout, "%s %s\n", SuccessIcon, message)
}

// Error prints an error message
func (s *StatusLine) Error(message string) {
	fmt.Fprintf(os.Stdout, "%s %s\n", ErrorIcon, message)
}

// Warning prints a warning message
func (s *StatusLine) Warning(message string) {
	fmt.Fprintf(os.Stdout, "%s %s\n", WarningIcon, message)
}

// Step prints a step indicator
func (s *StatusLine) Step(step, total int, message string) {
	fmt.Fprintf(os.Stdout, "[%d/%d] %s\n", step, total, message)
}

// PrintAuthHint prints a helpful message when SSH authentication fails
func PrintAuthHint(ip, command string) {
	status := NewStatusLine()
	status.Error("Authentication failed")
	fmt.Println()
	fmt.Printf("  Try: go_fastboot --username <user> --password <pass> --target %s %s\n", ip, command)
	fmt.Println()
}

// FormatTime formats a time.Time in a human-readable format
// Example: "Jan 30, 2026 5:26 PM"
func FormatTime(t time.Time) string {
	return t.Format("Jan 2, 2006 3:04 PM")
}
