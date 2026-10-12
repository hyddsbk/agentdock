package main

import (
	"context"

	"github.com/uvwt/agentdock/internal/nexusbridge"
	tooltask "github.com/uvwt/agentdock/internal/tool/task"
)

type nexusTrustCheckpointProvider struct {
	home string
}

func (p nexusTrustCheckpointProvider) FetchTrustCheckpoint(ctx context.Context, nodeID string) (tooltask.TrustCheckpointSnapshot, error) {
	snapshot, err := nexusbridge.FetchAuditTrustCheckpoint(ctx, p.home, nodeID)
	if err != nil {
		return tooltask.TrustCheckpointSnapshot{}, err
	}
	return tooltask.TrustCheckpointSnapshot{
		Checkpoint: snapshot.Checkpoint,
		Registry:   snapshot.Registry,
	}, nil
}
