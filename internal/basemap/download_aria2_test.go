package basemap

import (
	"crypto/md5"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestAria2ArgsIncludeResumeOptions(t *testing.T) {
	url := "https://example.com/20260401.pmtiles"
	dir := "/data/archives"
	out := "20260401.pmtiles.part"

	args := aria2Args(url, dir, out)

	want := []string{
		"--no-conf=true",
		"--continue=true",
		"--allow-overwrite=true",
		"--auto-file-renaming=false",
		"--file-allocation=none",
		"--max-tries=0",
		"--retry-wait=5",
		"--split=8",
		"--max-connection-per-server=8",
		"--min-split-size=8M",
		"--show-console-readout=false",
		"--summary-interval=0",
		"--console-log-level=warn",
		"--dir=" + dir,
		"--out=" + out,
		url,
	}

	if len(args) != len(want) {
		t.Fatalf("unexpected arg count: got %d want %d (%v)", len(args), len(want), args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("unexpected arg %d: got %q want %q (%v)", i, args[i], want[i], args)
		}
	}
}

func TestRestoreCompletedTempArchivePromotesAndRemovesControl(t *testing.T) {
	dir := t.TempDir()
	updater := NewUpdater(UpdaterConfig{DataDir: dir})

	sum := md5.Sum([]byte("abc"))
	latest := Build{
		Key:    "20260401.pmtiles",
		Size:   3,
		MD5Sum: base64.StdEncoding.EncodeToString(sum[:]),
	}

	archiveDir := filepath.Join(dir, ArchiveDirName)
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		t.Fatalf("mkdir archive dir: %v", err)
	}

	tempPath := filepath.Join(archiveDir, latest.Key+".part")
	finalPath := filepath.Join(archiveDir, latest.Key)

	if err := os.WriteFile(tempPath, []byte("abc"), 0o644); err != nil {
		t.Fatalf("write temp archive: %v", err)
	}
	if err := os.WriteFile(downloadControlPath(tempPath), []byte("state"), 0o644); err != nil {
		t.Fatalf("write control file: %v", err)
	}

	promoted, err := updater.restoreCompletedTempArchive(tempPath, finalPath, latest, Manifest{})
	if err != nil {
		t.Fatalf("restore completed archive: %v", err)
	}
	if !promoted {
		t.Fatalf("expected completed archive to be promoted")
	}

	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Fatalf("temp archive still exists: %v", err)
	}
	if _, err := os.Stat(downloadControlPath(tempPath)); !os.IsNotExist(err) {
		t.Fatalf("control file still exists: %v", err)
	}

	got, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatalf("read promoted archive: %v", err)
	}
	if string(got) != "abc" {
		t.Fatalf("unexpected promoted archive contents: %q", got)
	}

	manifest, err := readManifest(filepath.Join(dir, StateDirName, "current-build.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if manifest.Key != latest.Key {
		t.Fatalf("unexpected manifest key: got %s want %s", manifest.Key, latest.Key)
	}
}
