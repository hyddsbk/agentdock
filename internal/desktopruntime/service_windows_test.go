//go:build windows

package desktopruntime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRetryScheduledTaskStartRetriesIgnoredRunUntilHealthy(t *testing.T) {
	t.Parallel()

	runs := 0
	healthChecks := 0
	err := retryScheduledTaskStart(
		context.Background(),
		250*time.Millisecond,
		10*time.Millisecond,
		func() error {
			runs++
			return nil
		},
		func() bool {
			healthChecks++
			return runs >= 2
		},
		"http://127.0.0.1:8765/healthz",
	)
	if err != nil {
		t.Fatalf("retryScheduledTaskStart() error = %v", err)
	}
	if runs != 2 {
		t.Fatalf("scheduled task run attempts = %d, want 2", runs)
	}
	if healthChecks < 3 {
		t.Fatalf("health checks = %d, want repeated probing", healthChecks)
	}
}

func TestRetryScheduledTaskStartReturnsSchedulerErrorImmediately(t *testing.T) {
	t.Parallel()

	want := errors.New("task missing")
	runs := 0
	err := retryScheduledTaskStart(
		context.Background(),
		time.Second,
		10*time.Millisecond,
		func() error {
			runs++
			return want
		},
		func() bool { return false },
		"http://127.0.0.1:8765/healthz",
	)
	if !errors.Is(err, want) {
		t.Fatalf("retryScheduledTaskStart() error = %v, want %v", err, want)
	}
	if runs != 1 {
		t.Fatalf("scheduled task run attempts = %d, want 1", runs)
	}
}

func TestRetryScheduledTaskStartRetriesTransientSchedulerRefusal(t *testing.T) {
	t.Parallel()

	runs := 0
	err := retryScheduledTaskStart(
		context.Background(),
		250*time.Millisecond,
		10*time.Millisecond,
		func() error {
			runs++
			if runs == 1 {
				return errors.New("schtasks /Run failed: ERROR: The operator or administrator has refused the request. (0x800710E0)")
			}
			return nil
		},
		func() bool { return runs >= 2 },
		"http://127.0.0.1:8765/healthz",
	)
	if err != nil {
		t.Fatalf("retryScheduledTaskStart() error = %v", err)
	}
	if runs != 2 {
		t.Fatalf("scheduled task run attempts = %d, want 2", runs)
	}
}

func TestTransientScheduledTaskStartErrorRecognizesLocalizedRefusal(t *testing.T) {
	t.Parallel()

	for _, message := range []string{
		"ERROR: The operator or administrator has refused the request.",
		"错误: 操作员或系统管理员拒绝了请求。",
		"scheduler returned 0x800710E0",
	} {
		if !isTransientScheduledTaskStartError(errors.New(message)) {
			t.Fatalf("expected transient Task Scheduler refusal: %q", message)
		}
	}
	if isTransientScheduledTaskStartError(errors.New("ERROR: The system cannot find the file specified.")) {
		t.Fatal("permanent Task Scheduler error was classified as transient")
	}
}

func TestRetryScheduledTaskStartTimesOutWithHealthURL(t *testing.T) {
	t.Parallel()

	const healthURL = "http://127.0.0.1:8765/healthz"
	err := retryScheduledTaskStart(
		context.Background(),
		25*time.Millisecond,
		5*time.Millisecond,
		func() error { return nil },
		func() bool { return false },
		healthURL,
	)
	if err == nil || !strings.Contains(err.Error(), healthURL) {
		t.Fatalf("retryScheduledTaskStart() error = %v, want health timeout containing %q", err, healthURL)
	}
}
