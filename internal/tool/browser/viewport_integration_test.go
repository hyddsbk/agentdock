//go:build browser_integration

package browser

import (
	"context"
	"testing"
)

func TestBrowserIntegrationHonorsExactNarrowViewport(t *testing.T) {
	server := integrationServer(t)
	defer server.Close()

	service := newIntegrationService(t)
	started, err := service.start(context.Background(), StartRequest{
		URL: server.URL, Browser: BrowserAuto, Headless: true,
		Viewport: Viewport{Width: 390, Height: 844}, Timeout: integrationBrowserStartTimeout,
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	snapshot, err := service.snapshot(context.Background(), SnapshotRequest{
		SessionID: started.SessionID,
		Timeout:   integrationBrowserStartTimeout,
	})
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snapshot.Viewport.Width != 390 || snapshot.Viewport.Height != 844 {
		t.Fatalf("viewport=%#v want width=390 height=844", snapshot.Viewport)
	}
}
