package basemap

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"time"
)

const (
	ArchiveDirName = "archives"
	StateDirName   = "state"
	CurrentAlias   = "current"
)

type Build struct {
	Key      string    `json:"key"`
	Size     int64     `json:"size"`
	MD5Sum   string    `json:"md5sum,omitempty"`
	B3Sum    string    `json:"b3sum,omitempty"`
	Uploaded time.Time `json:"uploaded"`
	Version  string    `json:"version"`
}

type Manifest struct {
	Build
	DownloadURL  string    `json:"download_url"`
	DownloadedAt time.Time `json:"downloaded_at"`
	FilePath     string    `json:"file_path"`
}

type CurrentStatus struct {
	Manifest
	Available         bool   `json:"available"`
	CurrentRawURL     string `json:"current_raw_url"`
	CurrentTileJSON   string `json:"current_tilejson_url"`
	CurrentVersionURL string `json:"current_version_url"`
}

func tilesetName(key string) string {
	return strings.TrimSuffix(key, filepath.Ext(key))
}

type UpdaterConfig struct {
	DataDir         string
	MetadataURL     string
	DownloadBaseURL string
	UpdateInterval  time.Duration
}

type ProxyConfig struct {
	DataDir     string
	UpstreamURL string
	Listen      string
	APIKey      string
}

func WriteJSON(w interface{ Write([]byte) (int, error) }, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}
