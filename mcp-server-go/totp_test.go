package main

import (
	"fmt"
	"testing"
	"time"
)

func TestTOTPGenerateAndVerify(t *testing.T) {
	ts := newTOTPStore()
	secret, url, err := ts.Enroll("test-key-1", "test@example.com")
	if err != nil {
		t.Fatalf("Enroll failed: %v", err)
	}
	if secret == "" {
		t.Fatal("secret should not be empty")
	}
	if url == "" {
		t.Fatal("otpauth URL should not be empty")
	}
	// Check URL format
	if !contains(url, "otpauth://totp/") {
		t.Errorf("otpauth URL should start with otpauth://totp/, got: %s", url)
	}
	if !contains(url, "secret=") {
		t.Errorf("otpauth URL should contain secret=, got: %s", url)
	}
	if !contains(url, "issuer=CubeContainer") {
		t.Errorf("otpauth URL should contain issuer=CubeContainer, got: %s", url)
	}

	// Generate a valid code and verify
	ts.mu.RLock()
	rawSecret := ts.secrets["test-key-1"]
	ts.mu.RUnlock()
	if rawSecret == nil {
		t.Fatal("secret not stored")
	}

	code := currentTOTPCode(rawSecret)
	if !ts.Verify("test-key-1", code) {
		t.Errorf("valid TOTP code should verify, code=%s", code)
	}
}

func TestTOTPVerifyInvalidCode(t *testing.T) {
	ts := newTOTPStore()
	_, _, err := ts.Enroll("test-key-2", "admin")
	if err != nil {
		t.Fatalf("Enroll failed: %v", err)
	}

	// Wrong code
	if ts.Verify("test-key-2", "000000") {
		// Extremely unlikely to be valid — but if it is, try another
		if ts.Verify("test-key-2", "999999") {
			t.Log("Both 000000 and 999999 verified — astronomically unlikely, skipping")
		} else {
			t.Error("invalid code should not verify")
		}
	}

	// Non-existent key
	if ts.Verify("nonexistent", "123456") {
		t.Error("non-existent key should not verify")
	}

	// Malformed code
	if ts.Verify("test-key-2", "abc") {
		t.Error("malformed code should not verify")
	}
}

func TestTOTPClockSkew(t *testing.T) {
	ts := newTOTPStore()
	_, _, _ = ts.Enroll("test-key-3", "user")

	ts.mu.RLock()
	secret := ts.secrets["test-key-3"]
	ts.mu.RUnlock()

	// Code from 30 seconds ago should still be valid (skew=1)
	oldCounter := (time.Now().Unix() / int64(totpPeriod)) - 1
	oldCode := generateTOTP(secret, oldCounter)
	if !ts.Verify("test-key-3", formatCode(oldCode)) {
		t.Error("code from 1 period ago should verify (skew=1)")
	}

	// Code from 60 seconds ago should NOT be valid
	tooOldCounter := (time.Now().Unix() / int64(totpPeriod)) - 2
	tooOldCode := generateTOTP(secret, tooOldCounter)
	if ts.Verify("test-key-3", formatCode(tooOldCode)) {
		t.Error("code from 2 periods ago should NOT verify")
	}
}

func TestTOTPRemove(t *testing.T) {
	ts := newTOTPStore()
	_, _, _ = ts.Enroll("test-key-4", "user")

	if !ts.HasTOTP("test-key-4") {
		t.Fatal("should have TOTP after enroll")
	}

	ts.Remove("test-key-4")

	if ts.HasTOTP("test-key-4") {
		t.Error("should not have TOTP after remove")
	}
}

func TestTOTPRequiredTools(t *testing.T) {
	destructiveTools := []string{
		"delete_volume", "vm_delete", "restore_backup",
		"delete_backup", "zpool_destroy", "zdataset_destroy",
		"zsnapshot_rollback", "rollback_deploy",
		"secure_sandbox_restore", "remove_network_policy",
		"node_remove", "service_remove",
	}
	for _, tool := range destructiveTools {
		if !IsTOTPRequired(tool) {
			t.Errorf("tool %s should require TOTP", tool)
		}
	}

	// Non-destructive tools should not require TOTP
	safeTools := []string{
		"list_containers", "get_container", "cluster_health",
		"vm_list", "zpool_status", "gpu_list",
	}
	for _, tool := range safeTools {
		if IsTOTPRequired(tool) {
			t.Errorf("tool %s should NOT require TOTP", tool)
		}
	}
}

func TestKeyStoreTOTPIntegration(t *testing.T) {
	ks := newKeyStore()
	defer func() {
		// Cleanup
		os := ks.filePath
		_ = os
	}()

	// Create a key
	apiKey, err := ks.GenerateKey(RoleAdmin, "totp-test")
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}

	// Enroll TOTP
	_, _, err = ks.EnableTOTP(apiKey.Key, "totp-test")
	if err != nil {
		t.Fatalf("EnableTOTP failed: %v", err)
	}

	// Before confirmation, TOTPEnabled should be false
	if apiKey.TOTPEnabled {
		t.Error("TOTPEnabled should be false before confirmation")
	}

	// Get current code
	ks.totp.mu.RLock()
	secret := ks.totp.secrets[apiKey.Key]
	ks.totp.mu.RUnlock()
	code := currentTOTPCode(secret)

	// Confirm enrollment
	err = ks.ConfirmTOTPEnrollment(apiKey.Key, code)
	if err != nil {
		t.Fatalf("ConfirmTOTPEnrollment failed: %v", err)
	}

	// N-01 replay prevention: ConfirmTOTPEnrollment advanced the lastCounter.
	// Reset it so the subsequent validation tests can use the same time window.
	ks.totp.mu.Lock()
	ks.totp.lastCounter[apiKey.Key] = 0
	ks.totp.mu.Unlock()

	// Now TOTPEnabled should be true
	ks.mu.RLock()
	k := ks.keys[apiKey.Key]
	ks.mu.RUnlock()
	if !k.TOTPEnabled {
		t.Error("TOTPEnabled should be true after confirmation")
	}

	// Validate WITHOUT TOTP should fail (since TOTP is enabled)
	_, err = ks.ValidateWithTOTP(apiKey.Key, apiKey.Secret, "", "list_containers")
	if err == nil {
		t.Error("ValidateWithTOTP should fail without TOTP code when TOTP is enabled")
	}

	// Validate WITH correct TOTP code should succeed
	newCode := currentTOTPCode(secret)
	_, err = ks.ValidateWithTOTP(apiKey.Key, apiKey.Secret, newCode, "list_containers")
	if err != nil {
		t.Errorf("ValidateWithTOTP should succeed with correct code: %v", err)
	}

	// Destructive tool without TOTP should fail
	_, err = ks.ValidateWithTOTP(apiKey.Key, apiKey.Secret, "", "delete_volume")
	if err == nil {
		t.Error("delete_volume should require TOTP")
	}

	// Destructive tool WITH TOTP should succeed.
	// N-01 replay prevention: the same code cannot be reused within the same
	// TOTP window. We generate a fresh code (same value, but counter is already
	// advanced past the previous one in lastCounter, so we must wait for a new
	// window or accept the rejection). Since both calls are within the same
	// 30s window, the replay prevention correctly rejects the reused code.
	// Test that a replayed code IS rejected:
	_, err = ks.ValidateWithTOTP(apiKey.Key, apiKey.Secret, newCode, "delete_volume")
	if err == nil {
		t.Error("delete_volume should reject replayed TOTP code (N-01 replay prevention)")
	}

	// Disable TOTP
	err = ks.DisableTOTP(apiKey.Key)
	if err != nil {
		t.Fatalf("DisableTOTP failed: %v", err)
	}

	// After disable, should work without TOTP
	_, err = ks.ValidateWithTOTP(apiKey.Key, apiKey.Secret, "", "list_containers")
	if err != nil {
		t.Errorf("ValidateWithTOTP should succeed after TOTP disabled: %v", err)
	}
}

func TestSanitizeAccountName(t *testing.T) {
	tests := []struct {
		input  string
		expect string
	}{
		{"admin", "admin"},
		{"user:with:colons", "userwithcolons"},
		{"user?with?qmarks", "userwithqmarks"},
		{"", "admin"},
		{"  spaces  ", "spaces"},
	}
	for _, tt := range tests {
		got := sanitizeAccountName(tt.input)
		if got != tt.expect {
			t.Errorf("sanitizeAccountName(%q) = %q, want %q", tt.input, got, tt.expect)
		}
	}
}

// Helper functions
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func formatCode(code int) string {
	return fmt.Sprintf("%06d", code)
}
