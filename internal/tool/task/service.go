package task

import (
	"context"

	"github.com/uvwt/agentdock/internal/config"
	evolutionpkg "github.com/uvwt/agentdock/internal/evolution"
	"github.com/uvwt/agentdock/internal/execevidence"
	"github.com/uvwt/agentdock/internal/taskstate"
)

type ConfigProvider func() config.Config

type EvolutionProvider interface {
	Guidance(context.Context, taskstate.Task) ([]taskstate.EvolutionContextItem, error)
	Candidates(context.Context, taskstate.Task) ([]taskstate.EvolutionContextItem, error)
	ValidateBindings(context.Context, taskstate.Task, []taskstate.EvolutionBinding) ([]taskstate.EvolutionBinding, error)
	ResolveBindings(context.Context, taskstate.Task) error
}

type EvolutionEvidenceProvider interface {
	EvidenceRecords(context.Context, string) ([]evolutionpkg.Record, error)
}

type Service struct {
	config    ConfigProvider
	tasks     *taskstate.Store
	evolution EvolutionProvider
	receipts  *execevidence.Store
}

func New(configProvider ConfigProvider, tasks *taskstate.Store, evolution ...EvolutionProvider) *Service {
	service := &Service{config: configProvider, tasks: tasks}
	if len(evolution) > 0 {
		service.evolution = evolution[0]
	}
	return service
}

func (s *Service) WithExecutionEvidence(store *execevidence.Store) *Service {
	s.receipts = store
	return s
}
