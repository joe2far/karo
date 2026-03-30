package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	karoruntime "github.com/joe2far/karo/internal/runtime"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	kubeClient, err := karoruntime.NewKubernetesClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create Kubernetes client: %v\n", err)
		os.Exit(1)
	}

	server := karoruntime.NewMCPServer(kubeClient)

	// Start debug server in background
	debugPort := karoruntime.GetEnvOrDefault("KARO_DEBUG_PORT", "9091")
	debugServer := karoruntime.NewDebugServer(server)
	go func() {
		if err := debugServer.Serve(":" + debugPort); err != nil {
			fmt.Fprintf(os.Stderr, "Debug server error: %v\n", err)
		}
	}()

	// Run MCP server on stdio
	if err := server.Serve(ctx, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "MCP server error: %v\n", err)
		os.Exit(1)
	}
}
