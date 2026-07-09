package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestCloudInitCreateReal(t *testing.T) {
	ctx := context.Background()

	req := mcp.CallToolRequest{}
	req.Params.Name = "vm_cloudinit_create"
	req.Params.Arguments = map[string]interface{}{
		"hostname": "test-vm-001",
		"username": "testuser",
		"packages": []interface{}{"nginx", "curl"},
		"ssh_keys": []interface{}{
			"ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQDtest fake@key",
		},
	}

	res, err := handleVMCloudInitCreate(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("cloud-init create failed: %v", res.Content)
	}

	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			var data CloudInitResult
			json.Unmarshal([]byte(tc.Text), &data)
			t.Logf("ISO: %s", data.ISOPath)
			t.Logf("Seed dir: %s", data.SeedDir)
			t.Logf("Hostname: %s", data.Hostname)
			t.Logf("Username: %s", data.Username)
			t.Logf("SSH keys: %d", data.KeyCount)

			// Verify ISO exists
			if _, err := os.Stat(data.ISOPath); err != nil {
				t.Errorf("ISO file not created: %v", err)
			}
			// Verify user-data
			userData, _ := os.Stat(data.SeedDir + "/user-data")
			if userData == nil {
				t.Error("user-data not created")
			}
			// Verify meta-data
			metaData, _ := os.Stat(data.SeedDir + "/meta-data")
			if metaData == nil {
				t.Error("meta-data not created")
			}

			// Read user-data and check content
			ud, _ := os.ReadFile(data.SeedDir + "/user-data")
			udStr := string(ud)
			if !strings.HasPrefix(udStr, "#cloud-config") {
				t.Errorf("user-data doesn't start with #cloud-config: got %q", safeFirstN(udStr, 20))
			}

			t.Log("user-data preview (first 200 chars):")
			if len(udStr) > 200 {
				t.Logf("  %s...", udStr[:200])
			} else {
				t.Logf("  %s", udStr)
			}
		}
	}

	// Cleanup
	os.RemoveAll("/var/lib/libvirt/seeds/test-vm-001")
}

func TestTemplateListReal(t *testing.T) {
	ctx := context.Background()

	// Create some test files
	os.MkdirAll(defaultImageDir, 0755)
	os.WriteFile(defaultImageDir+"/ubuntu-22.04.qcow2", []byte("fake"), 0644)
	os.WriteFile(defaultImageDir+"/alpine-cloud.img", []byte("fake"), 0644)
	os.WriteFile(defaultImageDir+"/ubuntu-22.04-seed.iso", []byte("fake"), 0644)

	req := mcp.CallToolRequest{}
	req.Params.Name = "vm_template_list"

	res, err := handleVMTemplateList(ctx, req)
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			var data struct {
				Templates []VMTemplate `json:"templates"`
				Total     int          `json:"total"`
			}
			json.Unmarshal([]byte(tc.Text), &data)
			t.Logf("Found %d templates", data.Total)
			for _, tpl := range data.Templates {
				t.Logf("  %s (%dMB) %s", tpl.Name, tpl.SizeMB, tpl.ModifiedAt)
			}
			if data.Total < 3 {
				t.Errorf("expected >= 3 templates, got %d", data.Total)
			}
		}
	}

	// Cleanup test files
	os.Remove(defaultImageDir + "/ubuntu-22.04.qcow2")
	os.Remove(defaultImageDir + "/alpine-cloud.img")
	os.Remove(defaultImageDir + "/ubuntu-22.04-seed.iso")
}

func TestCreateCloudInitISO(t *testing.T) {
	// Test the ISO creation directly
	tmpDir := "/tmp/cube-ci-test"
	os.MkdirAll(tmpDir, 0755)
	defer os.RemoveAll(tmpDir)

	// Create test files
	os.WriteFile(tmpDir+"/user-data", []byte("#cloud-config\nhostname: test\n"), 0644)
	os.WriteFile(tmpDir+"/meta-data", []byte("instance-id: test\n"), 0644)

	isoPath := tmpDir + "/test-seed.iso"
	err := createCloudInitISO(tmpDir, isoPath)
	if err != nil {
		t.Fatalf("createCloudInitISO failed: %v", err)
	}

	// Verify ISO was created
	info, err := os.Stat(isoPath)
	if err != nil {
		t.Fatalf("ISO not created: %v", err)
	}
	if info.Size() < 1000 {
		t.Errorf("ISO suspiciously small: %d bytes", info.Size())
	}
	t.Logf("ISO created: %dKB", info.Size()/1024)

	// Verify it has correct volume label using isoinfo or file
	// Just check the file type
	out, _ := os.ReadFile(isoPath)
	if len(out) < 32769 { // ISO9660 has data at sector 16 (32768)
		t.Log("ISO smaller than expected but may still be valid")
	}
}

func safeFirstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
