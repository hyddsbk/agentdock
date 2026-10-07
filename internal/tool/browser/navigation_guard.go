package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// navigationGuard guards HTTP document redirect responses before the browser
// follows Location. This is a scheme policy, NOT a network/SSRF sandbox.
type navigationGuard struct {
	mu     sync.Mutex
	denied bool
}

func (g *navigationGuard) markDenied() {
	g.mu.Lock()
	g.denied = true
	g.mu.Unlock()
}

func (g *navigationGuard) takeDenied() bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	denied := g.denied
	g.denied = false
	g.mu.Unlock()
	return denied
}

func installNavigationGuard(pageCtx context.Context) (*navigationGuard, error) {
	guard := &navigationGuard{}
	chromedp.ListenTarget(pageCtx, func(event any) {
		paused, ok := event.(*fetch.EventRequestPaused)
		if !ok {
			return
		}
		// Never issue CDP commands directly in the event callback: doing so
		// would deadlock the target event dispatcher.
		go func() {
			ctx, cancel := context.WithTimeout(pageCtx, 10*time.Second)
			defer cancel()
			if redirectViolatesPolicy(paused) {
				guard.markDenied()
				_ = chromedp.Run(ctx, fetch.FailRequest(paused.RequestID, network.ErrorReasonBlockedByClient))
				return
			}
			if err := chromedp.Run(ctx, fetch.ContinueResponse(paused.RequestID)); err != nil {
				// Fail closed if the browser cannot safely resume a paused document.
				guard.markDenied()
				_ = chromedp.Run(ctx, fetch.FailRequest(paused.RequestID, network.ErrorReasonBlockedByClient))
			}
		}()
	})
	if err := chromedp.Run(pageCtx, fetch.Enable().WithPatterns([]*fetch.RequestPattern{
		{ResourceType: network.ResourceTypeDocument, RequestStage: fetch.RequestStageResponse},
	})); err != nil {
		return nil, err
	}
	return guard, nil
}

func redirectViolatesPolicy(ev *fetch.EventRequestPaused) bool {
	if ev == nil || ev.Request == nil {
		return true
	}
	if validateNavigationURL(ev.Request.URL, false) != nil {
		return true
	}
	switch ev.ResponseStatusCode {
	case 301, 302, 303, 307, 308:
	default:
		return false
	}
	var location string
	found := false
	for _, header := range ev.ResponseHeaders {
		if header != nil && strings.EqualFold(header.Name, "location") {
			if found {
				return true
			} // ambiguous duplicate Location headers
			found = true
			location = header.Value
		}
	}
	if !found {
		return false
	} // no redirect target to follow
	if location == "" || strings.TrimSpace(location) != location || strings.ContainsAny(location, "\r\n\t\\") {
		return true
	}
	base, baseErr := url.Parse(ev.Request.URL)
	target, targetErr := url.Parse(location)
	if baseErr != nil || targetErr != nil {
		return true
	}
	return validateNavigationURL(base.ResolveReference(target).String(), false) != nil
}

// clickNavigationTarget probes only the DOM link or native submit target.
// Script event handlers cannot be predicted and are not covered by this probe.
func clickNavigationTarget(parent, pageCtx context.Context, selector string) (string, error) {
	encoded, _ := json.Marshal(selector)
	expression := fmt.Sprintf(`(() => {
      const el = document.querySelector(%s);
      if (!el) return "";
      const anchor = el.closest('a[href],area[href]');
      if (anchor) return anchor.href || new URL(anchor.getAttribute('href'), document.baseURI).href;
      const submit = el.matches('button:not([type="button"]):not([type="reset"]),input[type="submit"],input[type="image"]');
      if (submit && el.form) return el.formAction || el.form.action || location.href;
      return "";
    })()`, encoded)
	var target string
	if err := runWithContext(parent, pageCtx, chromedp.Evaluate(expression, &target)); err != nil {
		return "", err
	}
	return target, nil
}

func preflightClickNavigation(parent, pageCtx context.Context, selector string) error {
	target, err := clickNavigationTarget(parent, pageCtx, selector)
	if err != nil {
		return err
	}
	if target == "" {
		return nil
	}
	if err := validateNavigationURL(target, true); err != nil {
		return browserError(ErrPolicyDenied, "click navigation is blocked by URL policy", "security_policy", &ErrorDetails{Field: "actions"}, nil)
	}
	return nil
}
