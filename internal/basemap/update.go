package basemap

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/zeebo/blake3"
)

func UpdaterConfigFromEnv() UpdaterConfig {
	return UpdaterConfig{
		DataDir:         envOr("DATA_DIR", "/data"),
		MetadataURL:     envOr("METADATA_URL", "https://build-metadata.protomaps.dev/builds.json"),
		DownloadBaseURL: envOr("DOWNLOAD_BASE_URL", "https://build.protomaps.com"),
		UpdateInterval:  mustDuration(envOr("UPDATE_INTERVAL", "24h")),
	}
}

func (c UpdaterConfig) normalize() UpdaterConfig {
	if c.DataDir == "" {
		c.DataDir = "/data"
	}
	if c.MetadataURL == "" {
		c.MetadataURL = "https://build-metadata.protomaps.dev/builds.json"
	}
	if c.DownloadBaseURL == "" {
		c.DownloadBaseURL = "https://build.protomaps.com"
	}
	if c.UpdateInterval <= 0 {
		c.UpdateInterval = 24 * time.Hour
	}
	return c
}

type Updater struct {
	cfg    UpdaterConfig
	client *http.Client
}

func NewUpdater(cfg UpdaterConfig) *Updater {
	cfg = cfg.normalize()
	return &Updater{
		cfg: cfg,
		client: &http.Client{
			Timeout: 0,
		},
	}
}

func (u *Updater) Watch(ctx context.Context, interval time.Duration) error {
	lock, err := u.lock(true)
	if err != nil {
		return err
	}
	defer lock.Close()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		if _, err := u.updateLocked(ctx, false); err != nil {
			fmt.Fprintf(os.Stderr, "update failed: %v\n", err)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (u *Updater) Update(ctx context.Context, force bool) (Build, error) {
	lock, err := u.lock(true)
	if err != nil {
		return Build{}, err
	}
	defer lock.Close()

	return u.updateLocked(ctx, force)
}

func (u *Updater) updateLocked(ctx context.Context, force bool) (Build, error) {

	if err := ensureDirs(u.cfg); err != nil {
		return Build{}, err
	}

	builds, err := u.fetchBuilds(ctx)
	if err != nil {
		return Build{}, err
	}
	latest := builds[0]

	manifestPath := u.manifestPath()
	current, _ := readManifest(manifestPath)
	finalPath := u.archivePath(latest.Key)
	tempPath := finalPath + ".part"

	if !force {
		if info, err := os.Stat(finalPath); err == nil {
			switch {
			case current.Key == latest.Key && info.Size() == latest.Size:
				removeDownloadArtifacts(tempPath)
				if err := u.writeCurrentManifest(latest); err != nil {
					return Build{}, err
				}
				return latest, nil
			case current.Key != latest.Key && info.Size() == latest.Size:
				if err := u.verifyArchive(finalPath, latest); err == nil {
					removeDownloadArtifacts(tempPath)
					if err := u.writeCurrentManifest(latest); err != nil {
						return Build{}, err
					}
					if current.Key != "" && current.Key != latest.Key {
						_ = os.Remove(u.archivePath(current.Key))
					}
					return latest, nil
				}
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return Build{}, err
		}

		if promoted, err := u.restoreCompletedTempArchive(tempPath, finalPath, latest, current); err != nil {
			return Build{}, err
		} else if promoted {
			return latest, nil
		}
	} else {
		removeDownloadArtifacts(tempPath)
	}

	if err := u.downloadLatest(ctx, latest, tempPath); err != nil {
		return Build{}, err
	}

	if err := u.verifyArchive(tempPath, latest); err != nil {
		removeDownloadArtifacts(tempPath)
		return Build{}, err
	}

	if err := os.Rename(tempPath, finalPath); err != nil {
		return Build{}, err
	}
	defer removeDownloadControl(tempPath)

	if err := u.writeCurrentManifest(latest); err != nil {
		return Build{}, err
	}

	if current.Key != "" && current.Key != latest.Key {
		_ = os.Remove(u.archivePath(current.Key))
	}

	return latest, nil
}

func (u *Updater) fetchBuilds(ctx context.Context) ([]Build, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.cfg.MetadataURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := u.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("metadata request failed: %s", resp.Status)
	}

	var builds []Build
	if err := json.NewDecoder(resp.Body).Decode(&builds); err != nil {
		return nil, err
	}
	if len(builds) == 0 {
		return nil, errors.New("metadata list is empty")
	}

	sort.Slice(builds, func(i, j int) bool {
		return builds[i].Key > builds[j].Key
	})
	return builds, nil
}

func (u *Updater) downloadLatestHTTP(ctx context.Context, build Build, tempPath string) (err error) {
	if err := os.MkdirAll(filepath.Dir(tempPath), 0o755); err != nil {
		return err
	}

	expected := build.Size
	var offset int64
	if info, err := os.Stat(tempPath); err == nil {
		offset = info.Size()
		if expected > 0 && offset > expected {
			if err := os.Remove(tempPath); err != nil {
				return err
			}
			offset = 0
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.downloadURL(build.Key), nil)
	if err != nil {
		return err
	}
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}

	resp, err := u.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	file, err := os.OpenFile(tempPath, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()

	progress := newDownloadProgress(build.Key, expected, 0, os.Stderr)
	mode := "starting"

	switch resp.StatusCode {
	case http.StatusOK:
		if err := file.Truncate(0); err != nil {
			return err
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return err
		}
	case http.StatusPartialContent:
		progress.initial = offset
		progress.bytes.Store(offset)
		progress.lastLogged = offset
		mode = "resuming"
		if offset == 0 {
			if err := file.Truncate(0); err != nil {
				return err
			}
		} else {
			if _, err := file.Seek(offset, io.SeekStart); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("download failed: %s", resp.Status)
	}

	if offset > 0 && resp.StatusCode == http.StatusOK {
		mode = "restarting"
	}

	progress.start(ctx, mode)
	defer func() {
		if err != nil {
			progress.finish("failed")
			return
		}
		progress.finish("completed")
	}()

	if _, err = io.Copy(file, &progressReader{reader: resp.Body, progress: progress}); err != nil {
		return err
	}

	if expected > 0 {
		if info, statErr := file.Stat(); statErr == nil && info.Size() != expected {
			err = fmt.Errorf("downloaded size mismatch: got %d want %d", info.Size(), expected)
			return err
		}
	}

	return file.Sync()
}

func (u *Updater) verifyArchive(path string, build Build) error {
	if build.B3Sum != "" {
		sum, err := blake3Sum(path)
		if err != nil {
			return err
		}
		if !strings.EqualFold(sum, build.B3Sum) {
			return fmt.Errorf("b3sum mismatch for %s: got %s want %s", build.Key, sum, build.B3Sum)
		}
		return nil
	}

	if build.MD5Sum != "" {
		sum, err := md5Base64(path)
		if err != nil {
			return err
		}
		if sum != build.MD5Sum {
			return fmt.Errorf("md5 mismatch for %s: got %s want %s", build.Key, sum, build.MD5Sum)
		}
		return nil
	}

	return nil
}

func blake3Sum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := blake3.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func md5Base64(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(h.Sum(nil)), nil
}

func readManifest(path string) (Manifest, error) {
	var manifest Manifest
	b, err := os.ReadFile(path)
	if err != nil {
		return manifest, err
	}
	if err := json.Unmarshal(b, &manifest); err != nil {
		return manifest, err
	}
	return manifest, nil
}

func writeManifest(path string, manifest Manifest) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if err := WriteJSON(f, manifest); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (u *Updater) writeCurrentManifest(build Build) error {
	return writeManifest(u.manifestPath(), Manifest{
		Build:        build,
		DownloadURL:  u.downloadURL(build.Key),
		DownloadedAt: time.Now().UTC(),
		FilePath:     relativeArchivePath(build.Key),
	})
}

func ensureDirs(cfg UpdaterConfig) error {
	for _, dir := range []string{
		cfg.DataDir,
		filepath.Join(cfg.DataDir, ArchiveDirName),
		filepath.Join(cfg.DataDir, StateDirName),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func (u *Updater) archivePath(key string) string {
	return filepath.Join(u.cfg.DataDir, ArchiveDirName, key)
}

func (u *Updater) manifestPath() string {
	return filepath.Join(u.cfg.DataDir, StateDirName, "current-build.json")
}

func (u *Updater) downloadURL(key string) string {
	return strings.TrimRight(u.cfg.DownloadBaseURL, "/") + "/" + key
}

func relativeArchivePath(key string) string {
	return filepath.ToSlash(filepath.Join(ArchiveDirName, key))
}

func LoadStatus(cfg UpdaterConfig) (CurrentStatus, error) {
	cfg = cfg.normalize()

	manifest, err := readManifest(filepath.Join(cfg.DataDir, StateDirName, "current-build.json"))
	if err != nil {
		return CurrentStatus{}, err
	}

	archivePath := filepath.Join(cfg.DataDir, ArchiveDirName, manifest.Key)
	_, statErr := os.Stat(archivePath)

	status := CurrentStatus{
		Manifest:          manifest,
		Available:         statErr == nil,
		CurrentRawURL:     "/" + CurrentAlias + ".pmtiles",
		CurrentTileJSON:   "/" + CurrentAlias + ".json",
		CurrentVersionURL: "/" + tilesetName(manifest.Key) + ".json",
	}
	return status, nil
}

func (u *Updater) lock(nonBlocking bool) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(u.lockPath()), 0o755); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(u.lockPath(), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}

	flag := syscall.LOCK_EX
	if nonBlocking {
		flag |= syscall.LOCK_NB
	}
	if err := syscall.Flock(int(file.Fd()), flag); err != nil {
		file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("another updater is already running")
		}
		return nil, err
	}
	return file, nil
}

func (u *Updater) lockPath() string {
	return filepath.Join(u.cfg.DataDir, StateDirName, ".update.lock")
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func mustDuration(v string) time.Duration {
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0
	}
	return d
}
