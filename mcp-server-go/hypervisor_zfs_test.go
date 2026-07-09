package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func makeZReq(name string, args map[string]interface{}) mcp.CallToolRequest {
	req := mcp.CallToolRequest{}
	req.Params.Name = name
	if args != nil {
		req.Params.Arguments = args
	}
	return req
}

func TestZFSAvailability(t *testing.T) {
	available := zfsAvailable()
	t.Logf("zfsAvailable() = %v", available)
	if !available {
		t.Log("ZFS CLI is available (binary installed) — kernel module is not (expected in WSL2)")
	}
}

func TestZPoolListNoKernel(t *testing.T) {
	// Even without kernel module, the handler should handle the error gracefully
	ctx := context.Background()
	req := makeZReq("zpool_list", nil)
	res, err := handleZPoolList(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	// Should return error result (not panic) because kernel module is missing
	if res.IsError {
		t.Logf("Expected error (no ZFS kernel module): handled gracefully")
		for _, c := range res.Content {
			if tc, ok := c.(mcp.TextContent); ok {
				t.Logf("  Error message: %s", tc.Text)
			}
		}
	} else {
		// If ZFS works, parse and log
		for _, c := range res.Content {
			if tc, ok := c.(mcp.TextContent); ok {
				var data struct {
					Pools []ZFSPool `json:"pools"`
					Total int       `json:"total"`
				}
				json.Unmarshal([]byte(tc.Text), &data)
				t.Logf("Found %d ZFS pools", data.Total)
			}
		}
	}
}

func TestHumanizeBytes(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"512", "512B"},
		{"1024", "1.0K"},
		{"1048576", "1.0M"},
		{"1073741824", "1.0G"},
		{"1099511627776", "1.0T"},
	}
	for _, tt := range tests {
		got := humanizeBytes(tt.input)
		if got != tt.want {
			t.Errorf("humanizeBytes(%q) = %q, want %q", tt.input, got, tt.want)
		}
		t.Logf("humanizeBytes(%s) = %s ✓", tt.input, got)
	}
}

// TestZFSHandlersDoNotPanic verifies all ZFS handlers handle missing ZFS gracefully
func TestZFSHandlersDoNotPanic(t *testing.T) {
	ctx := context.Background()

	handlers := []struct {
		name string
		args map[string]interface{}
		fn   func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)
	}{
		{"zpool_list", nil, handleZPoolList},
		{"zpool_status", nil, handleZPoolStatus},
		{"zdataset_list", nil, handleZDatasetList},
		{"zsnapshot_list", nil, handleZSnapshotList},
	}

	for _, h := range handlers {
		t.Run(h.name, func(t *testing.T) {
			req := makeZReq(h.name, h.args)
			res, err := h.fn(ctx, req)
			if err != nil {
				t.Errorf("%s returned error: %v", h.name, err)
			}
			if res == nil {
				t.Errorf("%s returned nil result", h.name)
			}
			t.Logf("%s: handled (isError=%v)", h.name, res.IsError)
		})
	}
}
