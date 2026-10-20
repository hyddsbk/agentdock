package skill

import (
	"context"
	"fmt"

	skillinterop "github.com/uvwt/agentdock/internal/skill/interop"
)

type discoveryProvider interface {
	Search(context.Context, string, string, int) ([]skillinterop.SearchCandidate, error)
	Pin(context.Context, string, string) (skillinterop.PinResult, error)
}

func newDiscoveryProvider() discoveryProvider {
	return skillinterop.NewDiscoveryClient(nil)
}

func (s *Service) Discovery(ctx context.Context, request DiscoveryRequest) (Result, error) {
	action := request.Action
	switch action {
	case "search":
		items, err := s.discovery.Search(ctx, request.Query, request.Owner, intValue(request.Limit, 10))
		if err != nil {
			return nil, toolErrorDetails("SKILL_DISCOVERY_SEARCH_FAILED", "search external Skill registry", "network", map[string]any{"reason": err.Error()})
		}
		return Result{
			"action":             "search",
			"query":              request.Query,
			"owner":              request.Owner,
			"count":              len(items),
			"candidates":         items,
			"install_authorized": false,
			"next_owner":         "project-due-diligence",
		}, nil
	case "pin":
		pinned, err := s.discovery.Pin(ctx, request.Source, request.Skill)
		if err != nil {
			return nil, toolErrorDetails("SKILL_DISCOVERY_PIN_FAILED", "pin external Skill source", "network", map[string]any{"reason": err.Error()})
		}
		return Result{
			"action":             "pin",
			"lock":               pinned.Lock,
			"lock_sha256":        pinned.LockSHA256,
			"resolved_at":        pinned.ResolvedAt,
			"review_required":    true,
			"install_authorized": false,
			"next_owner":         "project-due-diligence",
		}, nil
	default:
		return nil, toolErrorDetails("INVALID_ACTION", "unsupported skill_discovery action", "validation", map[string]any{
			"action":  fmt.Sprint(action),
			"allowed": []string{"search", "pin"},
		})
	}
}
