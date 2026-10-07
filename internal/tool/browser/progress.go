package browser

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

const (
	pageFingerprintVersion = 1
	fingerprintTextLimit   = 12000
	fingerprintInteractive = 80
	stallReplanThreshold   = 3
)

type browserProgressState struct {
	hasObserved           bool
	lastObserved          PageFingerprint
	lastActionSignature   string
	lastActionFingerprint string
	noProgressRepeats     int
}

func fingerprintFromDOM(state domSnapshot) PageFingerprint {
	text := truncateRunes(state.Text, fingerprintTextLimit)
	interactive := state.InteractiveElements
	if len(interactive) > fingerprintInteractive {
		interactive = interactive[:fingerprintInteractive]
	}
	fingerprint := PageFingerprint{
		Version:           pageFingerprintVersion,
		URL:               state.URL,
		Title:             state.Title,
		TextDigest:        digestString(text),
		InteractiveDigest: digestJSON(interactive),
		InteractiveCount:  len(state.InteractiveElements),
		ScrollX:           state.ScrollX,
		ScrollY:           state.ScrollY,
	}
	fingerprint.Digest = digestJSON(struct {
		Version           int    `json:"version"`
		URL               string `json:"url"`
		Title             string `json:"title"`
		TextDigest        string `json:"text_digest"`
		InteractiveDigest string `json:"interactive_digest"`
		InteractiveCount  int    `json:"interactive_count"`
		ScrollX           int    `json:"scroll_x"`
		ScrollY           int    `json:"scroll_y"`
	}{
		Version: fingerprint.Version, URL: fingerprint.URL, Title: fingerprint.Title,
		TextDigest: fingerprint.TextDigest, InteractiveDigest: fingerprint.InteractiveDigest,
		InteractiveCount: fingerprint.InteractiveCount, ScrollX: fingerprint.ScrollX, ScrollY: fingerprint.ScrollY,
	})
	return fingerprint
}

func digestString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func digestJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return digestString("")
	}
	return digestString(string(encoded))
}

func buildStateDelta(previous PageFingerprint, current PageFingerprint, hasPrevious bool) StateDelta {
	delta := StateDelta{
		HasPrevious:   hasPrevious,
		CurrentDigest: current.Digest,
	}
	if !hasPrevious {
		return delta
	}
	delta.PreviousDigest = previous.Digest
	delta.URLChanged = previous.URL != current.URL
	delta.TitleChanged = previous.Title != current.Title
	delta.TextChanged = previous.TextDigest != current.TextDigest
	delta.InteractiveChanged = previous.InteractiveDigest != current.InteractiveDigest ||
		previous.InteractiveCount != current.InteractiveCount
	delta.ScrollChanged = previous.ScrollX != current.ScrollX || previous.ScrollY != current.ScrollY
	delta.Changed = previous.Digest != current.Digest
	return delta
}

func (sess *session) observeFingerprint(current PageFingerprint) StateDelta {
	sess.progressMu.Lock()
	defer sess.progressMu.Unlock()
	delta := buildStateDelta(sess.progress.lastObserved, current, sess.progress.hasObserved)
	sess.progress.lastObserved = current
	sess.progress.hasObserved = true
	return delta
}

func (sess *session) finalizeActionProgress(actions []Action, before PageFingerprint, hasBefore bool, after PageFingerprint) (StateDelta, BrowserActionResult) {
	delta := buildStateDelta(before, after, hasBefore)
	result := BrowserActionResult{
		OK:           true,
		Status:       "completed",
		ActionCount:  len(actions),
		ActionKinds:  browserActionKinds(actions),
		StateChanged: delta.Changed,
	}

	sess.progressMu.Lock()
	defer sess.progressMu.Unlock()
	sess.progress.lastObserved = after
	sess.progress.hasObserved = true

	if !hasBefore {
		sess.resetActionStallLocked()
		return delta, result
	}
	if !actionsMayProgress(actions) {
		return delta, result
	}
	if delta.Changed {
		sess.resetActionStallLocked()
		return delta, result
	}

	result.Status = "no_progress"
	result.Reason = "state_unchanged"
	signature := actionSignature(actions)
	if signature == sess.progress.lastActionSignature && after.Digest == sess.progress.lastActionFingerprint {
		sess.progress.noProgressRepeats++
	} else {
		sess.progress.noProgressRepeats = 1
	}
	sess.progress.lastActionSignature = signature
	sess.progress.lastActionFingerprint = after.Digest
	result.StallCount = sess.progress.noProgressRepeats

	if sess.progress.noProgressRepeats >= stallReplanThreshold {
		result.Status = "stalled"
		result.LoopDetected = true
		result.ReplanRequired = true
		result.Reason = "repeated_no_progress"
		result.Replan = &ReplanSignal{
			Required:          true,
			Reason:            "repeated_no_progress",
			StallCount:        sess.progress.noProgressRepeats,
			SuggestedStrategy: "refresh_snapshot_and_choose_different_action",
		}
	}
	return delta, result
}

func (sess *session) resetActionStallLocked() {
	sess.progress.lastActionSignature = ""
	sess.progress.lastActionFingerprint = ""
	sess.progress.noProgressRepeats = 0
}

func browserActionKinds(actions []Action) []string {
	kinds := make([]string, 0, len(actions))
	for _, action := range actions {
		kinds = append(kinds, action.Kind)
	}
	return kinds
}

func actionsMayProgress(actions []Action) bool {
	for _, action := range actions {
		switch action.Kind {
		case "wait", "wait_for_selector", "wait_for_url", "wait_for_text", "wait_for_response":
			continue
		default:
			return true
		}
	}
	return false
}

func actionSignature(actions []Action) string {
	return digestJSON(actions)
}

func failedBrowserActionResult(actions []Action, reason string) BrowserActionResult {
	return BrowserActionResult{
		OK:          false,
		Status:      "failed",
		ActionCount: len(actions),
		ActionKinds: browserActionKinds(actions),
		Reason:      reason,
	}
}
