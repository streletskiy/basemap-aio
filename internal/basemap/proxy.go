package basemap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

func ProxyConfigFromEnv() ProxyConfig {
	return ProxyConfig{
		DataDir:     envOr("DATA_DIR", "/data"),
		UpstreamURL: envOr("UPSTREAM_URL", "http://tiles:8081"),
		Listen:      envOr("LISTEN", ":8080"),
		APIKey:      strings.TrimSpace(os.Getenv("API_KEY")),
	}
}

func (c ProxyConfig) normalize() ProxyConfig {
	if c.DataDir == "" {
		c.DataDir = "/data"
	}
	if c.UpstreamURL == "" {
		c.UpstreamURL = "http://tiles:8081"
	}
	if c.Listen == "" {
		c.Listen = ":8080"
	}
	return c
}

func RunProxy(ctx context.Context, cfg ProxyConfig) error {
	cfg = cfg.normalize()

	upstreamURL, err := url.Parse(cfg.UpstreamURL)
	if err != nil {
		return err
	}

	proxy := httputil.NewSingleHostReverseProxy(upstreamURL)
	proxy.Transport = &http.Transport{
		Proxy: http.ProxyFromEnvironment,
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, fmt.Sprintf("upstream unavailable: %v", err), http.StatusBadGateway)
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		if resp.StatusCode != http.StatusOK {
			return nil
		}
		if !strings.HasSuffix(resp.Request.URL.Path, ".json") {
			return nil
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		_ = resp.Body.Close()

		originalPath := resp.Request.Header.Get("X-Basemap-Original-Path")
		rewritten, err := rewriteTileJSON(body, originalPath, cfg.DataDir, cfg.APIKey)
		if err != nil {
			return err
		}

		resp.Body = io.NopCloser(bytes.NewReader(rewritten))
		resp.ContentLength = int64(len(rewritten))
		resp.Header.Set("Content-Length", fmt.Sprintf("%d", len(rewritten)))
		resp.Header.Set("Content-Type", "application/json")
		resp.Header.Del("Content-Encoding")
		return nil
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setCORS(w)

		switch {
		case r.URL.Path == "/healthz":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok\n"))
			return
		case r.URL.Path == "/status":
			writeStatus(w, cfg)
			return
		case r.Method == http.MethodOptions:
			w.WriteHeader(http.StatusNoContent)
			return
		}

		if err := authorize(r, cfg.APIKey); err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}

		if isPMTilesRequest(r.URL.Path) {
			servePMTilesFile(w, r, cfg.DataDir)
			return
		}

		if targetPath, ok, err := resolveCurrentAlias(r.URL.Path, cfg.DataDir); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				http.Error(w, "current build not ready", http.StatusServiceUnavailable)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		} else if ok {
			r2 := cloneRequest(r)
			r2.Header.Set("X-Basemap-Original-Path", r.URL.Path)
			r2.URL.Path = targetPath
			r2.URL.RawQuery = stripKeyQuery(r.URL.RawQuery)
			proxy.ServeHTTP(w, r2)
			return
		}

		r2 := cloneRequest(r)
		r2.Header.Set("X-Basemap-Original-Path", r.URL.Path)
		r2.URL.RawQuery = stripKeyQuery(r.URL.RawQuery)
		proxy.ServeHTTP(w, r2)
	})

	server := &http.Server{
		Addr:              cfg.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		_ = server.Shutdown(context.Background())
	}()

	err = server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func waitForManifest(ctx context.Context, dataDir string, timeout time.Duration) (Manifest, error) {
	deadline := time.Now().Add(timeout)
	manifestPath := filepath.Join(dataDir, StateDirName, "current-build.json")

	for {
		manifest, err := readManifest(manifestPath)
		if err == nil {
			return manifest, nil
		}
		if time.Now().After(deadline) {
			return Manifest{}, fmt.Errorf("timed out waiting for %s", manifestPath)
		}
		select {
		case <-ctx.Done():
			return Manifest{}, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func waitForUpstream(ctx context.Context, upstreamURL, tileset string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 5 * time.Second}
	target := strings.TrimRight(upstreamURL, "/") + "/" + tileset + ".json"

	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err == nil {
			resp, err := client.Do(req)
			if err == nil && resp.StatusCode == http.StatusOK {
				resp.Body.Close()
				return nil
			}
			if resp != nil {
				resp.Body.Close()
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for upstream tilejson %s", target)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func authorize(r *http.Request, apiKey string) error {
	if apiKey == "" {
		return nil
	}
	if r.URL.Query().Get("key") == apiKey {
		return nil
	}
	if r.Header.Get("X-API-Key") == apiKey {
		return nil
	}
	return errors.New("unauthorized")
}

func setCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "X-API-Key, Authorization, Content-Type")
	w.Header().Set("Access-Control-Allow-Methods", "GET,HEAD,OPTIONS")
}

func writeStatus(w http.ResponseWriter, cfg ProxyConfig) {
	manifest, err := readManifest(filepath.Join(cfg.DataDir, StateDirName, "current-build.json"))
	if err != nil {
		status := CurrentStatus{
			Available:         false,
			CurrentRawURL:     "/" + CurrentAlias + ".pmtiles",
			CurrentTileJSON:   "/" + CurrentAlias + ".json",
			CurrentVersionURL: "/" + CurrentAlias + ".json",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = WriteJSON(w, status)
		return
	}

	status := CurrentStatus{
		Manifest:          manifest,
		Available:         fileExists(filepath.Join(cfg.DataDir, ArchiveDirName, manifest.Key)),
		CurrentRawURL:     "/" + CurrentAlias + ".pmtiles",
		CurrentTileJSON:   "/" + CurrentAlias + ".json",
		CurrentVersionURL: "/" + tilesetName(manifest.Key) + ".json",
	}

	w.Header().Set("Content-Type", "application/json")
	_ = WriteJSON(w, status)
}

func isPMTilesRequest(p string) bool {
	return strings.HasSuffix(strings.ToLower(p), ".pmtiles")
}

func servePMTilesFile(w http.ResponseWriter, r *http.Request, dataDir string) {
	rel := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")

	switch {
	case rel == CurrentAlias+".pmtiles":
		manifest, err := readManifest(filepath.Join(dataDir, StateDirName, "current-build.json"))
		if err != nil {
			http.Error(w, "current build not ready", http.StatusServiceUnavailable)
			return
		}
		rel = filepath.ToSlash(filepath.Join(ArchiveDirName, manifest.Key))
	case strings.HasPrefix(rel, CurrentAlias+"/"):
		http.NotFound(w, r)
		return
	case strings.HasPrefix(rel, ArchiveDirName+"/"):
		// already rooted in the archive tree
	default:
		rel = filepath.ToSlash(filepath.Join(ArchiveDirName, rel))
	}

	full := filepath.Clean(filepath.Join(dataDir, filepath.FromSlash(rel)))
	base := filepath.Clean(dataDir) + string(os.PathSeparator)
	if full != filepath.Clean(dataDir) && !strings.HasPrefix(full, base) {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	if !fileExists(full) {
		http.NotFound(w, r)
		return
	}

	http.ServeFile(w, r, full)
}

func resolveCurrentAlias(requestPath, dataDir string) (string, bool, error) {
	if requestPath == "/" {
		return "", false, nil
	}

	rel := strings.TrimPrefix(path.Clean("/"+requestPath), "/")
	if rel == CurrentAlias+".json" {
		manifest, err := readManifest(filepath.Join(dataDir, StateDirName, "current-build.json"))
		if err != nil {
			return "", false, err
		}
		return "/" + tilesetName(manifest.Key) + ".json", true, nil
	}

	if strings.HasPrefix(rel, CurrentAlias+"/") {
		manifest, err := readManifest(filepath.Join(dataDir, StateDirName, "current-build.json"))
		if err != nil {
			return "", false, err
		}
		suffix := strings.TrimPrefix(rel, CurrentAlias)
		return "/" + tilesetName(manifest.Key) + suffix, true, nil
	}

	return "", false, nil
}

func rewriteTileJSON(body []byte, originalPath, dataDir, apiKey string) ([]byte, error) {
	if len(body) == 0 {
		return body, nil
	}

	manifest, err := readManifest(filepath.Join(dataDir, StateDirName, "current-build.json"))
	if err != nil {
		return nil, err
	}

	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, err
	}

	tiles, ok := doc["tiles"].([]any)
	if !ok {
		return body, nil
	}

	rewriteAlias := strings.HasPrefix(path.Clean("/"+originalPath), "/"+CurrentAlias)
	versionPrefix := "/" + tilesetName(manifest.Key)

	for i, raw := range tiles {
		s, ok := raw.(string)
		if !ok {
			continue
		}
		if rewriteAlias {
			s = strings.Replace(s, versionPrefix, "/"+CurrentAlias, 1)
		}
		if apiKey != "" {
			keyValue := url.QueryEscape(apiKey)
			if hasQueryParam(s, "key") {
				// The upstream response already carries a key parameter.
			} else if strings.Contains(s, "?") {
				s += "&key=" + keyValue
			} else {
				s += "?key=" + keyValue
			}
		}
		tiles[i] = s
	}

	doc["tiles"] = tiles

	out, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func cloneRequest(r *http.Request) *http.Request {
	r2 := r.Clone(r.Context())
	r2.Header = r.Header.Clone()
	return r2
}

func stripKeyQuery(raw string) string {
	if raw == "" {
		return ""
	}
	v, err := url.ParseQuery(raw)
	if err != nil {
		return raw
	}
	v.Del("key")
	return v.Encode()
}

func hasQueryParam(rawURL, name string) bool {
	idx := strings.IndexByte(rawURL, '?')
	if idx < 0 || idx == len(rawURL)-1 {
		return false
	}
	query := rawURL[idx+1:]
	for _, part := range strings.Split(query, "&") {
		if part == name || strings.HasPrefix(part, name+"=") {
			return true
		}
	}
	return false
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
