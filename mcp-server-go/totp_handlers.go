// Package main: TOTP management via MCP tools.
//
// These tools allow an admin to enroll, confirm, and disable TOTP
// second-factor authentication for API keys. The enrollment flow is:
//
//  1. totp_enroll → generates secret, returns QR-compatible otpauth:// URL
//  2. User scans QR with Google/Microsoft Authenticator
//  3. totp_confirm → user enters first 6-digit code to activate TOTP
//  4. totp_disable → admin can disable TOTP for a key (requires admin role)
package main

import (
	"fmt"
)

// ---- Result types ----

type TOTPEnrollResult struct {
	KeyID      string `json:"key_id"`
	Secret     string `json:"secret"`       // base32 secret (for manual entry)
	OTPAuthURL string `json:"otpauth_url"`  // for QR code generation
	QRMessage  string `json:"qr_message"`   // human-readable instructions
	Status     string `json:"status"`
}

type TOTPConfirmResult struct {
	KeyID  string `json:"key_id"`
	Status string `json:"status"`
}

type TOTPDisableResult struct {
	KeyID  string `json:"key_id"`
	Status string `json:"status"`
}

type TOTPStatusResult struct {
	KeyID       string `json:"key_id"`
	TOTPEnabled bool   `json:"totp_enabled"`
	HasSecret   bool   `json:"has_secret_enrolled"`
}

// ---- Handler functions (called from handlers_phase2.go) ----

func totpEnroll(keyID, accountName string) (*TOTPEnrollResult, error) {
	if keyStore == nil {
		return nil, fmt.Errorf("auth key store is not initialized")
	}
	if keyID == "" {
		return nil, fmt.Errorf("key_id is required")
	}
	if accountName == "" {
		accountName = "admin"
	}

	secret, url, err := keyStore.EnableTOTP(keyID, accountName)
	if err != nil {
		return nil, fmt.Errorf("TOTP enrollment failed: %w", err)
	}

	return &TOTPEnrollResult{
		KeyID:      keyID,
		Secret:     secret,
		OTPAuthURL: url,
		QRMessage:  "Scan this QR code with Google Authenticator or Microsoft Authenticator, then call totp_confirm with your first 6-digit code.",
		Status:     "pending_confirmation",
	}, nil
}

func totpConfirm(keyID, code string) (*TOTPConfirmResult, error) {
	if keyStore == nil {
		return nil, fmt.Errorf("auth key store is not initialized")
	}
	if keyID == "" {
		return nil, fmt.Errorf("key_id is required")
	}
	if code == "" {
		return nil, fmt.Errorf("code is required")
	}

	if err := keyStore.ConfirmTOTPEnrollment(keyID, code); err != nil {
		return nil, fmt.Errorf("TOTP confirmation failed: %w", err)
	}

	return &TOTPConfirmResult{
		KeyID:  keyID,
		Status: "totp_enabled",
	}, nil
}

func totpDisable(keyID string) (*TOTPDisableResult, error) {
	if keyStore == nil {
		return nil, fmt.Errorf("auth key store is not initialized")
	}
	if keyID == "" {
		return nil, fmt.Errorf("key_id is required")
	}

	if err := keyStore.DisableTOTP(keyID); err != nil {
		return nil, fmt.Errorf("failed to disable TOTP: %w", err)
	}

	return &TOTPDisableResult{
		KeyID:  keyID,
		Status: "totp_disabled",
	}, nil
}

func totpStatus(keyID string) (*TOTPStatusResult, error) {
	if keyStore == nil {
		return nil, fmt.Errorf("auth key store is not initialized")
	}
	if keyID == "" {
		return nil, fmt.Errorf("key_id is required")
	}

	keyStore.mu.RLock()
	k, exists := keyStore.keys[keyID]
	keyStore.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("key not found")
	}

	return &TOTPStatusResult{
		KeyID:       keyID,
		TOTPEnabled: k.TOTPEnabled,
		HasSecret:   keyStore.totp.HasTOTP(keyID),
	}, nil
}
