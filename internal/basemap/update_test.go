package basemap

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSelectLatestSortsByKey(t *testing.T) {
	builds := []Build{
		{Key: "20260101.pmtiles"},
		{Key: "20260401.pmtiles"},
		{Key: "20260301.pmtiles"},
	}

	got := append([]Build(nil), builds...)
	status := sortLatest(got)
	if status.Key != "20260401.pmtiles" {
		t.Fatalf("unexpected latest build: %s", status.Key)
	}
}

func TestRewriteTileJSONAddsAPIKeyAndCurrentAlias(t *testing.T) {
	dir := t.TempDir()
	cfg := UpdaterConfig{DataDir: dir}
	manifest := Manifest{
		Build: Build{
			Key:      "20260401.pmtiles",
			Uploaded: time.Now().UTC(),
			Version:  "4.14.4",
		},
	}

	if err := writeManifest(dir+"/state/current-build.json", manifest); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	body := []byte(`{"tiles":["http://localhost:8080/20260401/{z}/{x}/{y}.mvt"]}`)
	out, err := rewriteTileJSON(body, "/current.json", cfg.DataDir, "secret")
	if err != nil {
		t.Fatalf("rewriteTileJSON: %v", err)
	}

	if !strings.Contains(string(out), "/current/{z}/{x}/{y}.mvt?key=secret") {
		t.Fatalf("unexpected output: %s", out)
	}

	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
}

func sortLatest(builds []Build) Build {
	if len(builds) == 0 {
		return Build{}
	}
	for i := 0; i < len(builds)-1; i++ {
		for j := i + 1; j < len(builds); j++ {
			if builds[j].Key > builds[i].Key {
				builds[i], builds[j] = builds[j], builds[i]
			}
		}
	}
	return builds[0]
}
