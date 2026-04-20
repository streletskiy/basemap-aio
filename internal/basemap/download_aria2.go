package basemap

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func (u *Updater) downloadLatest(ctx context.Context, build Build, tempPath string) error {
	if _, err := exec.LookPath("aria2c"); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return u.downloadLatestHTTP(ctx, build, tempPath)
		}
		return err
	}

	if err := u.downloadLatestAria2(ctx, build, tempPath); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return u.downloadLatestHTTP(ctx, build, tempPath)
		}
		return err
	}
	return nil
}

func (u *Updater) downloadLatestAria2(ctx context.Context, build Build, tempPath string) (err error) {
	if err := os.MkdirAll(filepath.Dir(tempPath), 0o755); err != nil {
		return err
	}

	expected := build.Size
	initial := int64(0)
	if info, err := os.Stat(tempPath); err == nil {
		initial = info.Size()
		if expected > 0 && initial > expected {
			removeDownloadArtifacts(tempPath)
			initial = 0
		}
	}

	mode := "starting"
	if initial > 0 {
		mode = "resuming"
	}

	progress := newDownloadProgress(build.Key, expected, initial, os.Stderr)
	progress.start(ctx, mode)
	defer func() {
		if err != nil {
			progress.finish("failed")
			return
		}
		progress.finish("completed")
	}()

	args := aria2Args(u.downloadURL(build.Key), filepath.Dir(tempPath), filepath.Base(tempPath))
	cmd := exec.CommandContext(ctx, "aria2c", args...)

	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output

	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if info, statErr := os.Stat(tempPath); statErr == nil {
					progress.bytes.Store(info.Size())
				}
			case <-done:
				return
			case <-ctx.Done():
				return
			}
		}
	}()

	if err = cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		trimmed := strings.TrimSpace(output.String())
		if trimmed != "" {
			return fmt.Errorf("aria2c download failed for %s: %w: %s", build.Key, err, trimmed)
		}
		return fmt.Errorf("aria2c download failed for %s: %w", build.Key, err)
	}

	if info, statErr := os.Stat(tempPath); statErr == nil {
		progress.bytes.Store(info.Size())
	}

	return nil
}

func aria2Args(url, dir, out string) []string {
	return []string{
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
}

func (u *Updater) restoreCompletedTempArchive(tempPath, finalPath string, latest Build, current Manifest) (bool, error) {
	info, err := os.Stat(tempPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}

	if latest.Size > 0 && info.Size() > latest.Size {
		removeDownloadArtifacts(tempPath)
		return false, nil
	}
	if latest.Size > 0 && info.Size() != latest.Size {
		return false, nil
	}

	if err := u.verifyArchive(tempPath, latest); err != nil {
		removeDownloadArtifacts(tempPath)
		return false, nil
	}

	if err := os.Rename(tempPath, finalPath); err != nil {
		return false, err
	}
	defer removeDownloadControl(tempPath)

	if err := u.writeCurrentManifest(latest); err != nil {
		return false, err
	}

	if current.Key != "" && current.Key != latest.Key {
		_ = os.Remove(u.archivePath(current.Key))
	}

	return true, nil
}

func downloadControlPath(path string) string {
	return path + ".aria2"
}

func removeDownloadControl(path string) {
	_ = os.Remove(downloadControlPath(path))
}

func removeDownloadArtifacts(path string) {
	_ = os.Remove(path)
	removeDownloadControl(path)
}
