package main

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func makeReq(name string, args map[string]interface{}) mcp.CallToolRequest {
	req := mcp.CallToolRequest{}
	req.Params.Name = name
	if args != nil {
		req.Params.Arguments = args
	}
	return req
}

func getResultJSON(t *testing.T, res *mcp.CallToolResult) map[string]interface{} {
	t.Helper()
	if res.IsError {
		body := ""
		for _, c := range res.Content {
			if tc, ok := c.(mcp.TextContent); ok {
				body = tc.Text
			}
		}
		t.Fatalf("tool returned error: %s", body)
	}
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			var data map[string]interface{}
			if err := json.Unmarshal([]byte(tc.Text), &data); err != nil {
				t.Fatalf("failed to parse JSON: %v\nraw: %s", err, tc.Text)
			}
			return data
		}
	}
	t.Fatal("no content in result")
	return nil
}

func TestVMLifecycleReal(t *testing.T) {
	if !virshAvailable() {
		t.Skip("libvirt not available")
	}
	ctx := context.Background()
	vmName := "cube-test-vm"

	// Cleanup from previous runs (aggressive — snapshots, nvram, everything)
	runVirsh("destroy", vmName)
	snapOut, _ := runVirsh("snapshot-list", "--domain", vmName, "--name")
	for _, sn := range splitNonEmpty(snapOut) {
		runVirsh("snapshot-delete", "--domain", vmName, sn)
	}
	runVirsh("undefine", vmName, "--nvram")
	runVirsh("undefine", vmName)

	// 1. Create VM
	t.Run("Create", func(t *testing.T) {
		req := makeReq("vm_create", map[string]interface{}{
			"name":      vmName,
			"vcpu":      1,
			"memory_mb": 512,
			"disk_gb":   2,
		})
		res, err := handleVMCreate(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		data := getResultJSON(t, res)
		t.Logf("Create result: %v", data["message"])
		if msg, ok := data["message"].(string); ok {
			fmt.Printf("  %s\n", msg)
		}
	})

	// 2. List VMs
	t.Run("List", func(t *testing.T) {
		req := makeReq("vm_list", nil)
		res, err := handleVMList(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		data := getResultJSON(t, res)
		total := int(data["total"].(float64))
		t.Logf("Found %d VMs", total)
		if vms, ok := data["vms"].([]interface{}); ok {
			for _, v := range vms {
				if vm, ok := v.(map[string]interface{}); ok {
					fmt.Printf("  VM: %s state=%s vcpu=%.0f mem=%dKB\n",
						vm["name"], vm["state"], vm["vcpu"], int64(vm["memory_kb"].(float64)))
				}
			}
		}
		if total == 0 {
			t.Error("expected at least 1 VM after create")
		}
	})

	// 3. Get VM detail
	t.Run("Get", func(t *testing.T) {
		req := makeReq("vm_get", map[string]interface{}{"name": vmName})
		res, err := handleVMGet(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range res.Content {
			if tc, ok := c.(mcp.TextContent); ok {
				var vm VMInfo
				json.Unmarshal([]byte(tc.Text), &vm)
				t.Logf("VM detail: name=%s state=%s vcpu=%d mem=%dKB autostart=%v",
					vm.Name, vm.State, vm.VCPU, vm.MemoryKB, vm.Autostart)
				fmt.Printf("  state=%s vcpu=%d mem=%dKB\n", vm.State, vm.VCPU, vm.MemoryKB)
			}
		}
	})

	// 4. Pause
	t.Run("Pause", func(t *testing.T) {
		req := makeReq("vm_pause", map[string]interface{}{"name": vmName})
		res, err := handleVMPause(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		if res.IsError {
			t.Skipf("pause failed (may be expected without KVM): %v", res.Content)
		}
		t.Logf("VM paused")
	})

	// 5. Resume
	t.Run("Resume", func(t *testing.T) {
		req := makeReq("vm_resume", map[string]interface{}{"name": vmName})
		res, err := handleVMResume(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		if res.IsError {
			t.Skipf("resume failed: %v", res.Content)
		}
		t.Logf("VM resumed")
	})

	// 6. Stop
	t.Run("Stop", func(t *testing.T) {
		req := makeReq("vm_stop", map[string]interface{}{
			"name":  vmName,
			"force": "true",
		})
		res, err := handleVMStop(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		if res.IsError {
			t.Logf("stop returned error: %v", res.Content)
		} else {
			t.Logf("VM stopped")
		}
	})

	// 7. Start again
	t.Run("Start", func(t *testing.T) {
		req := makeReq("vm_start", map[string]interface{}{"name": vmName})
		res, err := handleVMStart(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		if res.IsError {
			t.Skipf("start failed (no KVM in WSL2): %v", res.Content)
		}
		t.Logf("VM started")
	})

	// 8. Snapshot create
	t.Run("SnapshotCreate", func(t *testing.T) {
		req := makeReq("vm_snapshot_create", map[string]interface{}{
			"name":          vmName,
			"snapshot_name": "snap1",
		})
		res, err := handleVMSnapshot(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		if res.IsError {
			t.Logf("snapshot create returned error (may need VM running): %v", res.Content)
		} else {
			t.Logf("Snapshot created")
		}
	})

	// 9. Snapshot list
	t.Run("SnapshotList", func(t *testing.T) {
		req := makeReq("vm_snapshot_list", map[string]interface{}{"name": vmName})
		res, err := handleVMSnapshotList(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		if !res.IsError {
			data := getResultJSON(t, res)
			t.Logf("Snapshots: %d", int(data["total"].(float64)))
		}
	})

	// 10. Delete VM
	t.Run("Delete", func(t *testing.T) {
		req := makeReq("vm_delete", map[string]interface{}{
			"name":        vmName,
			"remove_disk": "true",
		})
		res, err := handleVMDelete(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		if res.IsError {
			t.Errorf("delete failed: %v", res.Content)
		} else {
			t.Logf("VM deleted")
		}
	})

	// Verify it's gone
	t.Run("VerifyDeleted", func(t *testing.T) {
		req := makeReq("vm_get", map[string]interface{}{"name": vmName})
		res, _ := handleVMGet(ctx, req)
		if !res.IsError {
			t.Error("VM still exists after delete")
		} else {
			t.Logf("VM correctly removed")
		}
	})
}
