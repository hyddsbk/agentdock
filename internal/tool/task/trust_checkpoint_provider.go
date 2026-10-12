package task

import (
	"context"

	"github.com/uvwt/agentdock/internal/audittrust"
)

type TrustCheckpointSnapshot struct {
	Checkpoint audittrust.Checkpoint
	Registry   audittrust.Registry
}

type TrustCheckpointProvider interface {
	FetchTrustCheckpoint(context.Context, string) (TrustCheckpointSnapshot, error)
}
