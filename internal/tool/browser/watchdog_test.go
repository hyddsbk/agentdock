package browser

import (
	"context"
	"testing"
	"time"
)

func TestWatchdogRemovesIdleOwnedAndPreservesExternal(t *testing.T) {
	s := New(Config{AgentDockHome: t.TempDir(), WatchdogStaleAge: time.Minute}, nil)
	defer s.Close()
	now := time.Now()
	s.now = func() time.Time { return now }
	owned := &session{id: "owned", lastActivity: now.Add(-2 * time.Minute)}
	external := &session{id: "external", external: true, lastActivity: now.Add(-2 * time.Minute)}
	fresh := &session{id: "fresh", lastActivity: now}
	s.sessions[owned.id] = owned
	s.sessions[external.id] = external
	s.sessions[fresh.id] = fresh
	got := s.watchdogSweep()
	if got.RemovedCount != 1 || got.RemovedSessions[0] != "owned" {
		t.Fatalf("watchdog sweep %#v", got)
	}
	status := s.watchdogStatus()
	if status.RemovedTotal != 1 || status.SessionCount != 2 || status.StaleAgeMS != 60000 {
		t.Fatalf("watchdog status %#v", status)
	}
	if _, err := s.getSession("external"); err != nil {
		t.Fatalf("external browser must survive automated cleanup: %v", err)
	}
	manual := s.cleanupStale(CleanupRequest{MaxAge: time.Minute})
	if manual.RemovedCount != 1 || manual.RemovedSessions[0] != "external" {
		t.Fatalf("manual cleanup should retain original capability %#v", manual)
	}
}

func TestWatchdogSkipsBusyAndDoesNotEvictRecent(t *testing.T) {
	s := New(Config{AgentDockHome: t.TempDir(), WatchdogStaleAge: time.Minute}, nil)
	defer s.Close()
	now := time.Now()
	s.now = func() time.Time { return now }
	sess := &session{id: "busy", lastActivity: now.Add(-2 * time.Minute)}
	s.sessions[sess.id] = sess
	sess.opMu.Lock()
	got := s.watchdogSweep()
	if got.RemovedCount != 0 || s.watchdogStatus().BusySkipped != 1 {
		t.Fatalf("active session removed or busy counter wrong: %#v", s.watchdogStatus())
	}
	sess.touch(now)
	sess.opMu.Unlock()
	if got = s.watchdogSweep(); got.RemovedCount != 0 {
		t.Fatalf("recently active session evicted: %#v", got)
	}
}

func TestWatchdogTickerRunsAndStops(t *testing.T) {
	s := New(Config{AgentDockHome: t.TempDir(), WatchdogInterval: 10 * time.Millisecond, WatchdogStaleAge: time.Minute}, nil)
	defer s.Close()
	s.mu.Lock()
	s.sessions["idle"] = &session{id: "idle", lastActivity: time.Now().Add(-time.Hour)}
	s.mu.Unlock()
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if state := s.watchdogStatus(); state.RemovedTotal == 1 {
			if !state.Enabled || state.LastSweep == "" {
				t.Fatalf("bad watchdog status: %#v", state)
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if state := s.watchdogStatus(); state.RemovedTotal != 1 {
		t.Fatalf("background watchdog did not evict idle session: %#v", state)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if s.watchdogStatus().Enabled {
		t.Fatal("watchdog must be disabled after Close")
	}
	if got, err := s.HandleSession(context.Background(), map[string]any{"action": "watchdog_status"}); err != nil || got["browser_ok"] != true {
		t.Fatalf("status tool result %#v, error %v", got, err)
	}
}
