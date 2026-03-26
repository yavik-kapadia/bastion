// Package test contains integration and multi-stream tests for Bastion.
// Tests in this package spin up real SRT relay + API servers on random ports.
package test

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/yavik14/bastion/internal/api"
	"github.com/yavik14/bastion/internal/db"
	"github.com/yavik14/bastion/internal/metrics"
	"github.com/yavik14/bastion/internal/model"
	"github.com/yavik14/bastion/internal/relay"
	"github.com/yavik14/bastion/internal/ws"
)

// testServer bundles all Bastion components for use in integration tests.
type testServer struct {
	relay   *relay.Relay
	db      *db.DB
	api     *api.Server
	srtAddr string
	apiAddr string // includes http:// prefix
	cancel  context.CancelFunc
}

// newTestServer starts a Bastion server on random ports and returns it.
// All resources are cleaned up in t.Cleanup.
func newTestServer(t *testing.T) *testServer {
	t.Helper()

	srtPort := freePort(t)
	apiPort := freePort(t)
	srtAddr := fmt.Sprintf("127.0.0.1:%d", srtPort)
	apiAddr := fmt.Sprintf("127.0.0.1:%d", apiPort)

	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	// Create default admin.
	if err := database.Users.Create("admin", "admin", "test-password-99", model.RoleAdmin); err != nil {
		t.Fatalf("create admin: %v", err)
	}

	prom := metrics.NewProm()
	hub := ws.NewHub()

	r := relay.New(srtAddr, 256, nil, relay.Config{})
	apiSrv, err := api.NewServer(database, r, prom, hub, nil, "")
	if err != nil {
		t.Fatalf("new api server: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	go hub.Run(ctx)
	go func() {
		if err := r.Start(ctx); err != nil {
			t.Logf("relay stopped: %v", err)
		}
	}()
	go func() {
		apiSrv.Start(ctx, apiAddr, "*") //nolint:errcheck
	}()

	// Wait for API server (TCP) to be ready.
	waitReady(t, "tcp", apiAddr, 3*time.Second)

	// Give SRT UDP listener a moment to initialize.
	time.Sleep(200 * time.Millisecond)

	ts := &testServer{
		relay:   r,
		db:      database,
		api:     apiSrv,
		srtAddr: srtAddr,
		apiAddr: "http://" + apiAddr,
		cancel:  cancel,
	}
	t.Cleanup(func() {
		cancel()
		database.Close()
	})
	return ts
}

// freePort returns an available TCP port.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

// waitReady polls addr until it accepts a connection or timeout expires.
func waitReady(t *testing.T, network, addr string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout(network, addr, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Don't fatal — SRT UDP won't accept a plain TCP check; just log.
	t.Logf("waitReady: %s %s may not be fully up yet", network, addr)
}
