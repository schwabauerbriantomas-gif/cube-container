package main

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestGPUListReal(t *testing.T) {
	ctx := context.Background()
	req := mcp.CallToolRequest{}
	req.Params.Name = "gpu_list"

	res, err := handleGPUList(ctx, req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %v", res.Content)
	}

	// Parse result
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			var data struct {
				GPUs  []GPUInfo `json:"gpus"`
				Total int       `json:"total"`
			}
			if err := json.Unmarshal([]byte(tc.Text), &data); err != nil {
				t.Fatalf("failed to parse JSON: %v", err)
			}
			t.Logf("Detected %d GPUs", data.Total)
			for _, g := range data.GPUs {
				t.Logf("  GPU[%d]: %s (%s) %dMB PCI=%s driver=%s",
					g.Index, g.Name, g.Vendor, g.MemoryMB, g.PCIAddress, g.Driver)
			}
			if data.Total == 0 {
				t.Skip("no GPUs detected on this host")
			}
			// Verify we found the RTX 3090
			found := false
			for _, g := range data.GPUs {
				if g.Vendor == "nvidia" && g.MemoryMB >= 24000 {
					found = true
					if g.PCIAddress == "" {
						t.Error("NVIDIA GPU missing PCI address")
					}
					if g.Driver == "" {
						t.Error("NVIDIA GPU missing driver version")
					}
				}
			}
			if !found {
				t.Logf("WARNING: RTX 3090 not found in GPU list (may be expected in CI)")
			}
		}
	}
}

func TestGPUStatsReal(t *testing.T) {
	ctx := context.Background()
	req := mcp.CallToolRequest{}
	req.Params.Name = "gpu_stats"

	res, err := handleGPUStats(ctx, req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Skipf("GPU stats not available (no nvidia-smi): %v", res.Content)
	}

	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			var data struct {
				GPUs  []map[string]interface{} `json:"gpus"`
				Total int                      `json:"total"`
			}
			if err := json.Unmarshal([]byte(tc.Text), &data); err != nil {
				t.Fatalf("failed to parse JSON: %v", err)
			}
			t.Logf("Stats for %d GPUs", data.Total)
			for _, g := range data.GPUs {
				t.Logf("  %s: GPU=%.0f%% Mem=%.0f%% Temp=%.0f°C Power=%.1fW",
					g["name"], *ptrFloat(g["gpu_util_pct"]),
					*ptrFloat(g["mem_util_pct"]),
					float64(*ptrInt(g["temp_c"])),
					*ptrFloat(g["power_draw_w"]))
			}
		}
	}
}

func TestNormalizePCIAddress(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"01:00.0", "0000:01:00.0"},
		{"0000:01:00.0", "0000:01:00.0"},
		{"", ""},
	}
	for _, tt := range tests {
		got := normalizePCIAddress(tt.input)
		if got != tt.want {
			t.Errorf("normalizePCIAddress(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestExtractPCIComponents(t *testing.T) {
	bus, slot, fn := extractPCIComponents("0000:01:00.0")
	if bus != "0x01" || slot != "0x00" || fn != "0x0" {
		t.Errorf("extractPCIComponents: bus=%s slot=%s fn=%s, want 0x01 0x00 0x0", bus, slot, fn)
	}
	fmt.Printf("PCI components: bus=%s slot=%s fn=%s\n", bus, slot, fn)
}

func ptrFloat(v interface{}) *float64 {
	if v == nil {
		f := 0.0
		return &f
	}
	switch n := v.(type) {
	case float64:
		return &n
	}
	f := 0.0
	return &f
}

func ptrInt(v interface{}) *int {
	if v == nil {
		i := 0
		return &i
	}
	switch n := v.(type) {
	case float64:
		i := int(n)
		return &i
	}
	i := 0
	return &i
}
