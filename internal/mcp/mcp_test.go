package mcp

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/amir/web-fetch-server/internal/config"
)

func TestToolsRegistered(t *testing.T) {
	server := Build(config.Load())

	// Connect client in-memory
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), t1, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	session, err := client.Connect(context.Background(), t2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer session.Close()

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}

	names := map[string]bool{}
	for _, tool := range tools.Tools {
		names[tool.Name] = true
	}

	for _, want := range []string{"web_search", "web_fetch"} {
		if !names[want] {
			t.Errorf("expected tool %q to be registered, got %v", want, names)
		}
	}
}
