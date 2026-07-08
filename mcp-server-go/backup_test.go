package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBackupVolume_CreateAndRestore(t *testing.T) {
	dir := t.TempDir()
	dm := &DeployManager{
		VolumesRoot: filepath.Join(dir, "volumes"),
	}
	os.MkdirAll(dm.VolumesRoot, 0755)

	bm := &BackupManager{
		BackupRoot:  filepath.Join(dir, "backups"),
		VolumesRoot: dm.VolumesRoot,
		DeployMgr:   dm,
	}

	// Create a volume with files
	dm.CreateVolume("app-data")
	volPath := filepath.Join(dm.VolumesRoot, "app-data")
	os.WriteFile(filepath.Join(volPath, "config.json"), []byte(`{"key":"value"}`), 0644)
	os.MkdirAll(filepath.Join(volPath, "sub"), 0755)
	os.WriteFile(filepath.Join(volPath, "sub", "data.txt"), []byte("important data"), 0644)

	// Backup
	backup, err := bm.BackupVolume("app-data")
	if err != nil {
		t.Fatalf("backup failed: %v", err)
	}
	if backup.Type != "volume" {
		t.Errorf("expected type 'volume', got '%s'", backup.Type)
	}
	if backup.SHA256 == "" {
		t.Error("expected non-empty checksum")
	}
	if backup.FileCount != 2 {
		t.Errorf("expected 2 files, got %d", backup.FileCount)
	}
	t.Logf("✓ backup created: %s (%.2f MB, %d files, sha256=%s...)",
		backup.ID, backup.SizeMB, backup.FileCount, backup.SHA256[:12])

	// Verify backup file exists
	if _, err := os.Stat(backup.Path); os.IsNotExist(err) {
		t.Fatal("backup file not found on disk")
	}

	// Modify volume to simulate data loss
	os.RemoveAll(volPath)
	if _, err := os.Stat(volPath); err == nil {
		t.Fatal("volume should be deleted")
	}

	// Restore
	result, err := bm.RestoreBackup(backup.ID)
	if err != nil {
		t.Fatalf("restore failed: %v", err)
	}
	if !result.IntegrityOK {
		t.Error("integrity check should pass")
	}
	t.Logf("✓ restore completed: integrity verified")

	// Verify files are back
	data, err := os.ReadFile(filepath.Join(volPath, "config.json"))
	if err != nil {
		t.Fatalf("config.json not restored: %v", err)
	}
	if string(data) != `{"key":"value"}` {
		t.Errorf("config.json content mismatch: %s", data)
	}

	data2, err := os.ReadFile(filepath.Join(volPath, "sub", "data.txt"))
	if err != nil {
		t.Fatalf("data.txt not restored: %v", err)
	}
	if string(data2) != "important data" {
		t.Errorf("data.txt content mismatch: %s", data2)
	}
	t.Logf("✓ all files restored correctly")
}

func TestBackupVolume_NonexistentVolume(t *testing.T) {
	dir := t.TempDir()
	bm := &BackupManager{
		BackupRoot:  filepath.Join(dir, "backups"),
		VolumesRoot: filepath.Join(dir, "volumes"),
	}
	os.MkdirAll(bm.VolumesRoot, 0755)

	_, err := bm.BackupVolume("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent volume")
	}
}

func TestBackupIntegrityTamperDetection(t *testing.T) {
	dir := t.TempDir()
	dm := &DeployManager{VolumesRoot: filepath.Join(dir, "volumes")}
	os.MkdirAll(dm.VolumesRoot, 0755)
	bm := &BackupManager{
		BackupRoot:  filepath.Join(dir, "backups"),
		VolumesRoot: dm.VolumesRoot,
		DeployMgr:   dm,
	}

	dm.CreateVolume("important")
	volPath := filepath.Join(dm.VolumesRoot, "important")
	os.WriteFile(filepath.Join(volPath, "critical.dat"), []byte("DO NOT MODIFY"), 0644)

	backup, _ := bm.BackupVolume("important")

	// Tamper: append garbage to the backup file
	f, _ := os.OpenFile(backup.Path, os.O_APPEND|os.O_WRONLY, 0600)
	f.Write([]byte("TAMPERED"))
	f.Close()

	// Restore should detect integrity failure
	result, err := bm.RestoreBackup(backup.ID)
	if err != nil {
		t.Fatalf("restore should not error: %v", err)
	}
	if result.IntegrityOK {
		t.Error("integrity check should FAIL after tampering")
	}
	t.Logf("✓ tampering detected: integrity_ok=false")
}

func TestBackupListAndDelete(t *testing.T) {
	dir := t.TempDir()
	dm := &DeployManager{VolumesRoot: filepath.Join(dir, "volumes")}
	os.MkdirAll(dm.VolumesRoot, 0755)
	bm := &BackupManager{
		BackupRoot:  filepath.Join(dir, "backups"),
		VolumesRoot: dm.VolumesRoot,
		DeployMgr:   dm,
	}

	// Create multiple backups
	dm.CreateVolume("v1")
	os.WriteFile(filepath.Join(dm.VolumesRoot, "v1", "f.txt"), []byte("v1"), 0644)

	b1, _ := bm.BackupVolume("v1")
	b2, _ := bm.BackupVolume("v1")

	// List
	backups, err := bm.ListBackups()
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 2 {
		t.Fatalf("expected 2 backups, got %d", len(backups))
	}
	t.Logf("✓ listed %d backups", len(backups))

	// Delete one
	if err := bm.DeleteBackup(b1.ID); err != nil {
		t.Fatal(err)
	}

	// Verify it's gone
	backups, _ = bm.ListBackups()
	if len(backups) != 1 {
		t.Fatalf("expected 1 backup after delete, got %d", len(backups))
	}
	if backups[0].ID != b2.ID {
		t.Error("wrong backup survived deletion")
	}
	t.Logf("✓ delete worked, %d backups remain", len(backups))

	// Verify file removed from disk
	if _, err := os.Stat(b1.Path); err == nil {
		t.Error("backup file should be deleted from disk")
	}
}

func TestBackupPruneRetention(t *testing.T) {
	dir := t.TempDir()
	dm := &DeployManager{VolumesRoot: filepath.Join(dir, "volumes")}
	os.MkdirAll(dm.VolumesRoot, 0755)
	bm := &BackupManager{
		BackupRoot:  filepath.Join(dir, "backups"),
		VolumesRoot: dm.VolumesRoot,
		DeployMgr:   dm,
	}

	// Create 5 backups of the same volume
	dm.CreateVolume("rot")
	for i := 0; i < 5; i++ {
		os.WriteFile(filepath.Join(dm.VolumesRoot, "rot", "n.txt"), []byte("version"), 0644)
		bm.BackupVolume("rot")
	}

	// List all
	backups, _ := bm.ListBackups()
	if len(backups) != 5 {
		t.Fatalf("expected 5 backups, got %d", len(backups))
	}

	// Prune: keep only 2
	pruned, err := bm.PruneBackups(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(pruned) != 3 {
		t.Errorf("expected 3 pruned, got %d", len(pruned))
	}

	// Verify only 2 remain
	backups, _ = bm.ListBackups()
	if len(backups) != 2 {
		t.Errorf("expected 2 remaining, got %d", len(backups))
	}
	t.Logf("✓ pruned %d, %d remaining", len(pruned), len(backups))
}

func TestBackupRestoreWithSubdirectories(t *testing.T) {
	dir := t.TempDir()
	dm := &DeployManager{VolumesRoot: filepath.Join(dir, "volumes")}
	os.MkdirAll(dm.VolumesRoot, 0755)
	bm := &BackupManager{
		BackupRoot:  filepath.Join(dir, "backups"),
		VolumesRoot: dm.VolumesRoot,
		DeployMgr:   dm,
	}

	// Create nested structure
	dm.CreateVolume("nested")
	volPath := filepath.Join(dm.VolumesRoot, "nested")
	os.MkdirAll(filepath.Join(volPath, "a", "b", "c"), 0755)
	os.WriteFile(filepath.Join(volPath, "a", "b", "c", "deep.txt"), []byte("deep"), 0644)
	os.WriteFile(filepath.Join(volPath, "root.txt"), []byte("root"), 0644)

	backup, _ := bm.BackupVolume("nested")

	// Wipe
	os.RemoveAll(volPath)

	// Restore
	bm.RestoreBackup(backup.ID)

	// Verify deep file
	data, err := os.ReadFile(filepath.Join(volPath, "a", "b", "c", "deep.txt"))
	if err != nil {
		t.Fatalf("deep file not restored: %v", err)
	}
	if string(data) != "deep" {
		t.Error("content mismatch")
	}

	data2, _ := os.ReadFile(filepath.Join(volPath, "root.txt"))
	if string(data2) != "root" {
		t.Error("root file content mismatch")
	}
	t.Logf("✓ nested directories restored correctly")
}
