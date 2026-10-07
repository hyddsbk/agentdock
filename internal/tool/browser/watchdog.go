package browser

import "time"

// WatchdogStatus exposes aggregate lifecycle state without CDP URLs or secrets.
type WatchdogStatus struct {
	Enabled      bool   `json:"enabled"`
	IntervalMS   int64  `json:"interval_ms"`
	StaleAgeMS   int64  `json:"stale_age_ms"`
	SessionCount int    `json:"session_count"`
	LastSweep    string `json:"last_sweep,omitempty"`
	RemovedTotal int    `json:"removed_total"`
	BusySkipped  int    `json:"busy_skipped_total"`
}

func (s *Service) staleWatchdogAge() time.Duration {
	if s.cfg.WatchdogStaleAge > 0 {
		return s.cfg.WatchdogStaleAge
	}
	return defaultStaleAge
}

func (s *Service) runWatchdog() {
	ticker := time.NewTicker(s.cfg.WatchdogInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.watchdogSweep()
		case <-s.watchdogStop:
			return
		}
	}
}

func (s *Service) watchdogSweep() CleanupResult {
	removed, skipped := s.cleanupStaleFiltered(CleanupRequest{MaxAge: s.staleWatchdogAge()}, true)
	s.mu.Lock()
	s.watchdogLastSweep = s.now()
	s.watchdogRemoved += removed.RemovedCount
	s.watchdogBusy += skipped
	s.mu.Unlock()
	return removed
}

func (s *Service) watchdogStatus() WatchdogStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := WatchdogStatus{
		Enabled:      s.cfg.WatchdogInterval > 0 && !s.closed,
		IntervalMS:   s.cfg.WatchdogInterval.Milliseconds(),
		StaleAgeMS:   s.staleWatchdogAge().Milliseconds(),
		SessionCount: len(s.sessions),
		RemovedTotal: s.watchdogRemoved,
		BusySkipped:  s.watchdogBusy,
	}
	if !s.watchdogLastSweep.IsZero() {
		state.LastSweep = s.watchdogLastSweep.UTC().Format(time.RFC3339)
	}
	return state
}
