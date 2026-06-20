package pg

import (
	"strings"
	"testing"
)

func TestBuildLaunchCommand_NoAuth(t *testing.T) {
	ctx := Context{
		Name:     "atlas",
		Database: "main",
		Port:     5432,
		Endpoints: map[string]Endpoint{
			"read": {Host: "atlas.read.example", User: "ro"},
		},
	}
	got, err := buildLaunchCommand(ctx, "read", "pgcli")
	if err != nil {
		t.Fatalf("buildLaunchCommand: %v", err)
	}
	if !strings.HasPrefix(got, `pgcli "postgresql://ro`) {
		t.Fatalf("expected pgcli URI prefix, got %q", got)
	}
	if !strings.Contains(got, "atlas.read.example:5432") {
		t.Fatalf("missing host:port in: %q", got)
	}
	if !strings.HasSuffix(got, "/main\"") {
		t.Fatalf("missing database in: %q", got)
	}
}

func TestBuildLaunchCommand_AuthWrapped(t *testing.T) {
	ctx := Context{
		Name:     "prod",
		Database: "main",
		AuthCmd:  "aws-vault exec prod --",
		Endpoints: map[string]Endpoint{
			"write": {Host: "h", User: "u"},
		},
	}
	got, err := buildLaunchCommand(ctx, "write", "pgcli")
	if err != nil {
		t.Fatalf("buildLaunchCommand: %v", err)
	}
	if !strings.HasPrefix(got, "aws-vault exec prod -- sh -c ") {
		t.Fatalf("expected auth-wrapped command, got %q", got)
	}
}

func TestBuildLaunchCommand_UnknownEndpoint(t *testing.T) {
	ctx := Context{Endpoints: map[string]Endpoint{"read": {Host: "x", User: "u"}}}
	if _, err := buildLaunchCommand(ctx, "write", "pgcli"); err == nil {
		t.Fatalf("expected error for unknown endpoint")
	}
}

func TestFlattenEndpoints_OneLinePerEndpoint(t *testing.T) {
	contexts := []Context{
		{Name: "a", Endpoints: map[string]Endpoint{"read": {}, "write": {}}},
		{Name: "b", Endpoints: map[string]Endpoint{"read": {}}},
	}
	lines, lookup := flattenEndpoints(contexts)
	if len(lines) != 3 {
		t.Fatalf("expected 3 endpoints, got %d", len(lines))
	}
	if len(lookup) != 3 {
		t.Fatalf("lookup should have 3 entries, got %d", len(lookup))
	}
	for _, l := range lines {
		entry, ok := lookup[l]
		if !ok || entry.Ctx == nil {
			t.Fatalf("line %q not resolvable in lookup", l)
		}
	}
}

func TestBuildLaunchCommand_DefaultPort(t *testing.T) {
	ctx := Context{
		Database:  "main",
		Endpoints: map[string]Endpoint{"r": {Host: "h", User: "u"}},
	}
	got, _ := buildLaunchCommand(ctx, "r", "pgcli")
	if !strings.Contains(got, ":5432") {
		t.Fatalf("expected default port 5432, got %q", got)
	}
}
