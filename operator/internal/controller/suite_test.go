package controller

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	karov1 "github.com/joe2far/karo/operator/api/v1"
)

// envtest harness shared by the integration tests. It is deliberately gated: a
// real apiserver/etcd (the setup-envtest binaries) is only present when
// KUBEBUILDER_ASSETS points at them. Plain `go test ./...` in CI has no such
// binaries, so we SKIP cleanly instead of failing — the unit tests in
// agentteam_controller_test.go still exercise the pure logic.
var (
	testEnv       *envtest.Environment
	testCfg       *rest.Config
	testClient    client.Client
	envtestActive bool
)

// envtestUnavailable reports whether the envtest apiserver is up. Each
// integration test calls this and skips when false, keeping `go test ./...`
// green without KUBEBUILDER_ASSETS.
func requireEnvtest(t *testing.T) {
	t.Helper()
	if !envtestActive {
		t.Skip("envtest unavailable; set KUBEBUILDER_ASSETS to run integration tests")
	}
}

func TestMain(m *testing.M) {
	// Only attempt to start envtest when the assets are advertised. setup-envtest
	// exports KUBEBUILDER_ASSETS; absent it, env.Start() would fail trying to exec
	// a nonexistent kube-apiserver.
	if os.Getenv("KUBEBUILDER_ASSETS") != "" {
		if code, ok := startEnvtest(m); ok {
			os.Exit(code)
		}
	}
	// No assets (or start failed): run the suite with envtest disabled so the
	// integration tests skip themselves.
	envtestActive = false
	os.Exit(m.Run())
}

// startEnvtest brings up the apiserver, registers the scheme, builds a client,
// runs the suite, then tears down. Returns (exitCode, true) on success, or
// (_, false) when the environment could not be started so the caller can fall
// back to the skip path.
func startEnvtest(m *testing.M) (int, bool) {
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd")},
		ErrorIfCRDPathMissing: true,
	}

	cfg, err := testEnv.Start()
	if err != nil || cfg == nil {
		return 0, false
	}
	testCfg = cfg

	if err := karov1.AddToScheme(scheme.Scheme); err != nil {
		_ = testEnv.Stop()
		return 0, false
	}

	cl, err := client.New(cfg, client.Options{Scheme: scheme.Scheme})
	if err != nil {
		_ = testEnv.Stop()
		return 0, false
	}
	testClient = cl
	envtestActive = true

	code := m.Run()
	_ = testEnv.Stop()
	return code, true
}

// newReconciler builds an AgentTeamReconciler backed by the envtest client.
func newReconciler() *AgentTeamReconciler {
	return &AgentTeamReconciler{Client: testClient, Scheme: scheme.Scheme}
}

// ctx is a convenience background context for the integration tests.
func testContext() context.Context { return context.Background() }
