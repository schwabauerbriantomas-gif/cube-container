// Package main: MCP tool handlers for Proxmox VE backend.
//
// These tools expose Proxmox VE operations through the MCP protocol.
// When the Proxmox backend is configured (CUBE_PROXMOX_HOST env var),
// an LLM agent can manage VMs, LXC containers, storage, and snapshots
// on a Proxmox cluster — all through the same MCP interface as local
// Docker/libvirt operations.
//
// Tool naming convention: pve_* (Proxmox VE) to distinguish from
// local hypervisor tools (vm_*, zpool_*).
package main

import (
	"context"
	"fmt"
	"net/url"

	"github.com/mark3labs/mcp-go/mcp"
)

// ---- VM lifecycle handlers ----

func handlePVEListVMs(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if proxmoxBackend == nil {
		return errResult("Proxmox backend is not configured. Set CUBE_PROXMOX_HOST and CUBE_PROXMOX_TOKEN environment variables."), nil
	}
	vms, err := proxmoxBackend.ListVMs()
	if err != nil {
		return unwrapError(err), nil
	}
	return okResult(map[string]interface{}{
		"vms":   vms,
		"total": len(vms),
	}), nil
}

func handlePVEGetVM(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if proxmoxBackend == nil {
		return errResult("Proxmox backend is not configured."), nil
	}
	args := parseArgs(req)
	node := argString(args, "node")
	vmid := argInt(args, "vmid", 0)
	if node == "" {
		node = proxmoxBackend.node
	}
	if node == "" {
		return errResult("node is required (or set CUBE_PROXMOX_NODE)"), nil
	}
	if vmid == 0 {
		return errResult("vmid is required"), nil
	}
	vm, err := proxmoxBackend.GetVM(node, vmid)
	if err != nil {
		return unwrapError(err), nil
	}
	return okResult(vm), nil
}

func handlePVECreateVM(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if proxmoxBackend == nil {
		return errResult("Proxmox backend is not configured."), nil
	}
	args := parseArgs(req)
	node := argString(args, "node")
	if node == "" {
		node = proxmoxBackend.node
	}
	if node == "" {
		return errResult("node is required (or set CUBE_PROXMOX_NODE)"), nil
	}

	name := argString(args, "name")
	if name == "" {
		return errResult("name is required"), nil
	}
	vmid := argInt(args, "vmid", 0)

	params := url.Values{}
	if vmid > 0 {
		params.Set("vmid", fmt.Sprintf("%d", vmid))
	}
	params.Set("name", name)
	params.Set("memory", fmt.Sprintf("%d", argInt(args, "memory_mb", 512)))
	params.Set("cores", fmt.Sprintf("%d", argInt(args, "cores", 2)))
	params.Set("sockets", "1")

	// Disk configuration
	diskSize := argInt(args, "disk_gb", 0)
	if diskSize == 0 {
		diskSize = 20
	}
	storage := argString(args, "storage")
	if storage == "" {
		storage = "local-lvm"
	}
	params.Set("scsi0", fmt.Sprintf("%s:%d", storage, diskSize))
	params.Set("scsihw", "virtio-scsi-single")
	params.Set("net0", "virtio,bridge=vmbr0")

	newVMID, err := proxmoxBackend.CreateVM(node, params)
	if err != nil {
		return unwrapError(err), nil
	}
	return okResult(map[string]interface{}{
		"vmid":  newVMID,
		"name":  name,
		"node":  node,
		"status": "created",
	}), nil
}

func handlePVEStartVM(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if proxmoxBackend == nil {
		return errResult("Proxmox backend is not configured."), nil
	}
	args := parseArgs(req)
	node := argString(args, "node")
	if node == "" {
		node = proxmoxBackend.node
	}
	vmid := argInt(args, "vmid", 0)
	if vmid == 0 {
		return errResult("vmid is required"), nil
	}
	if err := proxmoxBackend.StartVM(node, vmid); err != nil {
		return unwrapError(err), nil
	}
	return okResult(map[string]interface{}{"vmid": vmid, "status": "starting"}), nil
}

func handlePVEStopVM(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if proxmoxBackend == nil {
		return errResult("Proxmox backend is not configured."), nil
	}
	args := parseArgs(req)
	node := argString(args, "node")
	if node == "" {
		node = proxmoxBackend.node
	}
	vmid := argInt(args, "vmid", 0)
	if vmid == 0 {
		return errResult("vmid is required"), nil
	}
	if err := proxmoxBackend.StopVM(node, vmid); err != nil {
		return unwrapError(err), nil
	}
	return okResult(map[string]interface{}{"vmid": vmid, "status": "stopped"}), nil
}

func handlePVEDeleteVM(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if proxmoxBackend == nil {
		return errResult("Proxmox backend is not configured."), nil
	}
	args := parseArgs(req)
	node := argString(args, "node")
	if node == "" {
		node = proxmoxBackend.node
	}
	vmid := argInt(args, "vmid", 0)
	if vmid == 0 {
		return errResult("vmid is required"), nil
	}
	if err := proxmoxBackend.DeleteVM(node, vmid); err != nil {
		return unwrapError(err), nil
	}
	return okResult(map[string]interface{}{"vmid": vmid, "status": "deleted"}), nil
}

func handlePVEMigrateVM(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if proxmoxBackend == nil {
		return errResult("Proxmox backend is not configured."), nil
	}
	args := parseArgs(req)
	node := argString(args, "node")
	if node == "" {
		return errResult("source node is required"), nil
	}
	vmid := argInt(args, "vmid", 0)
	if vmid == 0 {
		return errResult("vmid is required"), nil
	}
	target := argString(args, "target_node")
	if target == "" {
		return errResult("target_node is required"), nil
	}
	online := argBool(args, "online")
	if err := proxmoxBackend.MigrateVM(node, vmid, target, online); err != nil {
		return unwrapError(err), nil
	}
	return okResult(map[string]interface{}{
		"vmid":        vmid,
		"from_node":   node,
		"target_node": target,
		"status":      "migrating",
	}), nil
}

// ---- Snapshot handlers ----

func handlePVEListSnapshots(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if proxmoxBackend == nil {
		return errResult("Proxmox backend is not configured."), nil
	}
	args := parseArgs(req)
	node := argString(args, "node")
	if node == "" {
		node = proxmoxBackend.node
	}
	vmid := argInt(args, "vmid", 0)
	if vmid == 0 {
		return errResult("vmid is required"), nil
	}
	snaps, err := proxmoxBackend.ListSnapshots(node, vmid)
	if err != nil {
		return unwrapError(err), nil
	}
	return okResult(map[string]interface{}{
		"snapshots": snaps,
		"total":     len(snaps),
	}), nil
}

func handlePVECreateSnapshot(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if proxmoxBackend == nil {
		return errResult("Proxmox backend is not configured."), nil
	}
	args := parseArgs(req)
	node := argString(args, "node")
	if node == "" {
		node = proxmoxBackend.node
	}
	vmid := argInt(args, "vmid", 0)
	if vmid == 0 {
		return errResult("vmid is required"), nil
	}
	name := argString(args, "name")
	if name == "" {
		return errResult("name is required"), nil
	}
	description := argString(args, "description")
	includeRAM := argBool(args, "include_ram")
	if err := proxmoxBackend.CreateSnapshot(node, vmid, name, description, includeRAM); err != nil {
		return unwrapError(err), nil
	}
	return okResult(map[string]interface{}{
		"vmid":   vmid,
		"name":   name,
		"status": "snapshot_creating",
	}), nil
}

func handlePVERestoreSnapshot(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if proxmoxBackend == nil {
		return errResult("Proxmox backend is not configured."), nil
	}
	args := parseArgs(req)
	node := argString(args, "node")
	if node == "" {
		node = proxmoxBackend.node
	}
	vmid := argInt(args, "vmid", 0)
	if vmid == 0 {
		return errResult("vmid is required"), nil
	}
	snapName := argString(args, "snapshot")
	if snapName == "" {
		return errResult("snapshot name is required"), nil
	}
	if err := proxmoxBackend.RestoreSnapshot(node, vmid, snapName); err != nil {
		return unwrapError(err), nil
	}
	return okResult(map[string]interface{}{
		"vmid":     vmid,
		"snapshot": snapName,
		"status":   "restoring",
	}), nil
}

func handlePVEDeleteSnapshot(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if proxmoxBackend == nil {
		return errResult("Proxmox backend is not configured."), nil
	}
	args := parseArgs(req)
	node := argString(args, "node")
	if node == "" {
		node = proxmoxBackend.node
	}
	vmid := argInt(args, "vmid", 0)
	if vmid == 0 {
		return errResult("vmid is required"), nil
	}
	snapName := argString(args, "snapshot")
	if snapName == "" {
		return errResult("snapshot name is required"), nil
	}
	if err := proxmoxBackend.DeleteSnapshot(node, vmid, snapName); err != nil {
		return unwrapError(err), nil
	}
	return okResult(map[string]interface{}{
		"vmid":     vmid,
		"snapshot": snapName,
		"status":   "deleted",
	}), nil
}

// ---- Storage & node handlers ----

func handlePVEListStorage(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if proxmoxBackend == nil {
		return errResult("Proxmox backend is not configured."), nil
	}
	storage, err := proxmoxBackend.ListStorage()
	if err != nil {
		return unwrapError(err), nil
	}
	return okResult(map[string]interface{}{
		"storage": storage,
		"total":   len(storage),
	}), nil
}

func handlePVEListNodes(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if proxmoxBackend == nil {
		return errResult("Proxmox backend is not configured."), nil
	}
	nodes, err := proxmoxBackend.ListNodes()
	if err != nil {
		return unwrapError(err), nil
	}
	return okResult(map[string]interface{}{
		"nodes": nodes,
		"total": len(nodes),
	}), nil
}

// ---- TOTP handlers (MCP tool interface) ----

func handleTOTPEnroll(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := parseArgs(req)
	keyID := argString(args, "key_id")
	if keyID == "" {
		return errResult("key_id is required"), nil
	}
	accountName := argString(args, "account_name")
	data, err := totpEnroll(keyID, accountName)
	if err != nil {
		return unwrapError(err), nil
	}
	return okResult(data), nil
}

func handleTOTPConfirm(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := parseArgs(req)
	keyID := argString(args, "key_id")
	if keyID == "" {
		return errResult("key_id is required"), nil
	}
	code := argString(args, "code")
	if code == "" {
		return errResult("code is required"), nil
	}
	data, err := totpConfirm(keyID, code)
	if err != nil {
		return unwrapError(err), nil
	}
	return okResult(data), nil
}

func handleTOTPDisable(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := parseArgs(req)
	keyID := argString(args, "key_id")
	if keyID == "" {
		return errResult("key_id is required"), nil
	}
	data, err := totpDisable(keyID)
	if err != nil {
		return unwrapError(err), nil
	}
	return okResult(data), nil
}

func handleTOTPStatus(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := parseArgs(req)
	keyID := argString(args, "key_id")
	if keyID == "" {
		return errResult("key_id is required"), nil
	}
	data, err := totpStatus(keyID)
	if err != nil {
		return unwrapError(err), nil
	}
	return okResult(data), nil
}
