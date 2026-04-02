package basemap

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
)

type previewTile struct {
	Z      int
	X      int
	Y      int
	Has    bool
	Box    [4]float64
	RawURL string
}

type previewPageData struct {
	TileJSONURL string
	APIKey      string
	Tile        previewTile
}

func servePreviewPage(w http.ResponseWriter, r *http.Request, cfg ProxyConfig) {
	cfg = cfg.normalize()

	tile := parsePreviewTile(r.URL.Path, r.URL.Query())
	tile.RawURL = "/" + CurrentAlias + ".pmtiles"
	if tile.Has {
		tile.RawURL = fmt.Sprintf("/%s/%d/%d/%d.mvt", CurrentAlias, tile.Z, tile.X, tile.Y)
	}
	if cfg.APIKey != "" {
		tile.RawURL += "?key=" + url.QueryEscape(cfg.APIKey)
	}

	tileJSONURL := "/" + CurrentAlias + ".json"
	if cfg.APIKey != "" {
		tileJSONURL += "?key=" + url.QueryEscape(cfg.APIKey)
	}

	html, err := renderPreviewPage(previewPageData{
		TileJSONURL: tileJSONURL,
		APIKey:      cfg.APIKey,
		Tile:        tile,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(html)
}

func parsePreviewTile(requestPath string, query url.Values) previewTile {
	tile := previewTile{}

	z, x, y, ok := parseTileQuery(query)
	if !ok {
		z, x, y, ok = parseTilePath(requestPath)
	}
	if ok {
		tile.Has = true
		tile.Z = z
		tile.X = x
		tile.Y = y
		tile.Box = tileBounds(z, x, y)
	}
	return tile
}

func parseTileQuery(query url.Values) (int, int, int, bool) {
	z, ok := parseIntValue(query.Get("z"))
	if !ok {
		return 0, 0, 0, false
	}
	x, ok := parseIntValue(query.Get("x"))
	if !ok {
		return 0, 0, 0, false
	}
	y, ok := parseIntValue(query.Get("y"))
	if !ok {
		return 0, 0, 0, false
	}
	return z, x, y, true
}

func parseTilePath(requestPath string) (int, int, int, bool) {
	rel := strings.TrimPrefix(path.Clean("/"+requestPath), "/preview")
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" {
		return 0, 0, 0, false
	}

	parts := strings.Split(rel, "/")
	if len(parts) < 3 {
		return 0, 0, 0, false
	}

	z, ok := parseIntPart(parts[0])
	if !ok {
		return 0, 0, 0, false
	}
	x, ok := parseIntPart(parts[1])
	if !ok {
		return 0, 0, 0, false
	}
	y, ok := parseIntPart(strings.TrimSuffix(parts[2], ".mvt"))
	if !ok {
		return 0, 0, 0, false
	}

	return z, x, y, true
}

func parseIntValue(v string) (int, bool) {
	if v == "" {
		return 0, false
	}
	return parseIntPart(v)
}

func parseIntPart(v string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return 0, false
	}
	return n, true
}

func tileBounds(z, x, y int) [4]float64 {
	n := math.Exp2(float64(z))
	minLon := float64(x)/n*360.0 - 180.0
	maxLon := float64(x+1)/n*360.0 - 180.0
	maxLat := tileYToLat(y, z)
	minLat := tileYToLat(y+1, z)
	return [4]float64{minLon, minLat, maxLon, maxLat}
}

func tileYToLat(y, z int) float64 {
	n := math.Pi - 2.0*math.Pi*float64(y)/math.Exp2(float64(z))
	return math.Atan(math.Sinh(n)) * 180.0 / math.Pi
}

func renderPreviewPage(data previewPageData) ([]byte, error) {
	pageJSON, err := jsonForPreview(data)
	if err != nil {
		return nil, err
	}

	const previewHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>basemap-aio preview</title>
  <link rel="stylesheet" href="https://unpkg.com/maplibre-gl@latest/dist/maplibre-gl.css">
  <style>
    :root {
      color-scheme: dark;
      --bg: #0a1020;
      --panel: rgba(9, 14, 26, 0.84);
      --text: #eef2ff;
      --accent: #60a5fa;
    }
    html, body {
      margin: 0;
      width: 100%;
      height: 100%;
      background: radial-gradient(circle at top, #16213d, var(--bg) 65%);
      color: var(--text);
      overflow: hidden;
      font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
    }
    #map {
      position: absolute;
      inset: 0;
    }
    .panel {
      position: absolute;
      top: 16px;
      left: 16px;
      z-index: 2;
      max-width: min(720px, calc(100vw - 32px));
      padding: 14px 16px;
      border: 1px solid rgba(255,255,255,0.10);
      border-radius: 14px;
      background: var(--panel);
      box-shadow: 0 16px 48px rgba(0, 0, 0, 0.35);
      backdrop-filter: blur(10px);
    }
    .title {
      font-size: 16px;
      font-weight: 700;
      margin: 0 0 6px;
    }
    .meta {
      margin: 0;
      font-size: 13px;
      line-height: 1.5;
      color: rgba(238,242,255,0.82);
      word-break: break-word;
    }
    .meta code, .meta a {
      color: var(--accent);
    }
    .badge {
      display: inline-block;
      margin-left: 8px;
      padding: 2px 8px;
      border-radius: 999px;
      background: rgba(96,165,250,0.18);
      color: #bfdbfe;
      font-size: 12px;
      vertical-align: middle;
    }
    .note {
      margin-top: 8px;
      font-size: 12px;
      color: rgba(238,242,255,0.66);
    }
    .inspector {
      margin-top: 12px;
      padding-top: 12px;
      border-top: 1px solid rgba(255,255,255,0.10);
    }
    .inspector-title {
      margin: 0 0 6px;
      font-size: 13px;
      font-weight: 700;
      color: #e0f2fe;
      text-transform: uppercase;
      letter-spacing: 0.04em;
    }
    .inspector-summary {
      margin: 0;
      font-size: 13px;
      line-height: 1.5;
      color: rgba(238,242,255,0.85);
    }
    .tag-list {
      margin-top: 10px;
      display: grid;
      gap: 8px;
      max-height: 300px;
      overflow: auto;
      padding-right: 4px;
    }
    .tag-row {
      display: grid;
      gap: 4px;
      padding: 8px 10px;
      border-radius: 10px;
      background: rgba(255,255,255,0.05);
      border: 1px solid rgba(255,255,255,0.06);
    }
    .tag-key {
      font-size: 11px;
      color: #93c5fd;
      text-transform: uppercase;
      letter-spacing: 0.04em;
      font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
    }
    .tag-value {
      font-size: 13px;
      color: #f8fafc;
      white-space: pre-wrap;
      word-break: break-word;
      font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
    }
    .tag-empty {
      font-size: 13px;
      color: rgba(238,242,255,0.65);
    }
  </style>
</head>
<body>
  <div id="map"></div>
  <div class="panel">
    <div class="title">basemap-aio tile preview <span class="badge">MapLibre</span></div>
    <p class="meta" id="coords"></p>
    <p class="meta">Raw tile: <a id="raw-link" href="#">open /current/{z}/{x}/{y}.mvt</a></p>
    <p class="meta">TileJSON: <code id="tilejson-link"></code></p>
    <div class="inspector">
      <div class="inspector-title">Building inspector</div>
      <p class="inspector-summary" id="feature-summary">Click a building footprint to inspect its OSM tags.</p>
      <div class="tag-list" id="feature-tags"></div>
    </div>
    <div class="note">This page renders the vector tile with a generic preview style. The raw <code>.mvt</code> link still downloads the binary tile.</div>
  </div>

  <script>
    window.__BASEMAP_PREVIEW__ = __BASEMAP_PREVIEW_DATA__;
  </script>
  <script src="https://unpkg.com/maplibre-gl@latest/dist/maplibre-gl.js"></script>
  <script>
    (async function () {
      const cfg = window.__BASEMAP_PREVIEW__;
      const headers = {};
      if (cfg.apiKey) {
        headers["X-API-Key"] = cfg.apiKey;
      }

      const resp = await fetch(cfg.tilejson_url, { headers: headers });
      if (!resp.ok) {
        throw new Error("TileJSON request failed: " + resp.status + " " + resp.statusText);
      }
      const tilejson = await resp.json();

      document.getElementById("tilejson-link").textContent = cfg.tilejson_url;
      const rawLink = document.getElementById("raw-link");
      rawLink.href = cfg.raw_url;
      rawLink.textContent = cfg.raw_url;

      const coords = document.getElementById("coords");
      if (cfg.tile && cfg.tile.has) {
        coords.innerHTML = "Tile <code>" +
          cfg.tile.z + "/" + cfg.tile.x + "/" + cfg.tile.y +
          "</code> bounds <code>" +
          cfg.tile.bounds[0].toFixed(5) + ", " +
          cfg.tile.bounds[1].toFixed(5) + ", " +
          cfg.tile.bounds[2].toFixed(5) + ", " +
          cfg.tile.bounds[3].toFixed(5) +
          "</code>";
      } else {
        coords.innerHTML = "Showing the current basemap. Add <code>/preview/{z}/{x}/{y}</code> to focus a specific tile.";
      }

      const palette = [
        "#60a5fa",
        "#f97316",
        "#34d399",
        "#f472b6",
        "#facc15",
        "#a78bfa",
        "#22d3ee",
        "#fb7185"
      ];
      const interactiveLayerIds = [];
      const buildingLayerIds = [];

      const layers = [{
        id: "preview-background",
        type: "background",
        paint: {
          "background-color": "#08111f"
        }
      }];

      if (Array.isArray(tilejson.vector_layers)) {
        tilejson.vector_layers.forEach(function (layer, index) {
          const color = palette[index % palette.length];
          const layerId = String(layer.id || ("layer-" + index)).replace(/[^a-zA-Z0-9_-]/g, "-");
          const fillId = layerId + "-fill";
          const lineId = layerId + "-line";
          const pointId = layerId + "-point";
          interactiveLayerIds.push(fillId, lineId, pointId);
          if (/building/i.test(String(layer.id || ""))) {
            buildingLayerIds.push(fillId, lineId, pointId);
          }
          layers.push({
            id: fillId,
            type: "fill",
            source: "basemap",
            "source-layer": layer.id,
            filter: ["==", ["geometry-type"], "Polygon"],
            paint: {
              "fill-color": color,
              "fill-opacity": 0.22
            }
          });
          layers.push({
            id: lineId,
            type: "line",
            source: "basemap",
            "source-layer": layer.id,
            filter: ["==", ["geometry-type"], "LineString"],
            paint: {
              "line-color": color,
              "line-width": 1.2
            }
          });
          layers.push({
            id: pointId,
            type: "circle",
            source: "basemap",
            "source-layer": layer.id,
            filter: ["==", ["geometry-type"], "Point"],
            paint: {
              "circle-color": color,
              "circle-radius": 2.5,
              "circle-stroke-color": "#ffffff",
              "circle-stroke-width": 0.5
            }
          });
        });
      }

      function escapeHtml(value) {
        return String(value)
          .replaceAll("&", "&amp;")
          .replaceAll("<", "&lt;")
          .replaceAll(">", "&gt;")
          .replaceAll("\"", "&quot;")
          .replaceAll("'", "&#39;");
      }

      function formatValue(value) {
        if (value === null || value === undefined) {
          return "";
        }
        if (Array.isArray(value)) {
          return "[" + value.map(formatValue).join(", ") + "]";
        }
        if (typeof value === "object") {
          try {
            return JSON.stringify(value);
          } catch (err) {
            return String(value);
          }
        }
        return String(value);
      }

      function showNoFeature(message) {
        document.getElementById("feature-summary").innerHTML = escapeHtml(message);
        document.getElementById("feature-tags").innerHTML = '<div class="tag-empty">' + escapeHtml(message) + '</div>';
      }

      function renderFeature(feature) {
        const summary = document.getElementById("feature-summary");
        const tags = document.getElementById("feature-tags");
        const properties = feature && feature.properties ? feature.properties : {};
        const entries = Object.entries(properties).sort(function (a, b) {
          return a[0].localeCompare(b[0]);
        });
        const geometryType = feature && feature.geometry && feature.geometry.type ? feature.geometry.type : "unknown";
        const sourceLayer = feature && feature.sourceLayer ? feature.sourceLayer : "unknown";
        const layerId = feature && feature.layer && feature.layer.id ? feature.layer.id : "unknown";
        const featureId = feature && feature.id !== undefined && feature.id !== null ? String(feature.id) : "n/a";

        summary.innerHTML = "Layer <code>" + escapeHtml(layerId) + "</code>, source-layer <code>" + escapeHtml(sourceLayer) + "</code>, geometry <code>" + escapeHtml(geometryType) + "</code>, feature id <code>" + escapeHtml(featureId) + "</code>.";

        if (!entries.length) {
          tags.innerHTML = '<div class="tag-empty">No properties found on this feature.</div>';
          return;
        }

        tags.innerHTML = entries.map(function (entry) {
          return '<div class="tag-row">' +
            '<div class="tag-key">' + escapeHtml(entry[0]) + '</div>' +
            '<div class="tag-value">' + escapeHtml(formatValue(entry[1])) + '</div>' +
            '</div>';
        }).join("");
      }

      const sources = {
        basemap: {
          type: "vector",
          tiles: tilejson.tiles,
          minzoom: tilejson.minzoom,
          maxzoom: tilejson.maxzoom,
          bounds: tilejson.bounds,
          attribution: tilejson.attribution
        }
      };

      if (cfg.tile && cfg.tile.has) {
        const b = cfg.tile.bounds;
        const previewBounds = {
          type: "Feature",
          properties: {},
          geometry: {
            type: "Polygon",
            coordinates: [[
              [b[0], b[1]],
              [b[2], b[1]],
              [b[2], b[3]],
              [b[0], b[3]],
              [b[0], b[1]]
            ]]
          }
        };

        sources["tile-boundary"] = {
          type: "geojson",
          data: previewBounds
        };

        layers.splice(1, 0, {
          id: "tile-boundary-fill",
          type: "fill",
          source: "tile-boundary",
          paint: {
            "fill-color": "#f8fafc",
            "fill-opacity": 0.04
          }
        }, {
          id: "tile-boundary-line",
          type: "line",
          source: "tile-boundary",
          paint: {
            "line-color": "#f8fafc",
            "line-width": 2
          }
        });
      }

      const map = new maplibregl.Map({
        container: "map",
        style: {
          version: 8,
          sources: sources,
          layers: layers
        },
        center: cfg.tile && cfg.tile.has ? [
          (cfg.tile.bounds[0] + cfg.tile.bounds[2]) / 2,
          (cfg.tile.bounds[1] + cfg.tile.bounds[3]) / 2
        ] : [0, 20],
        zoom: cfg.tile && cfg.tile.has ? Math.min(cfg.tile.z + 0.55, 22) : 1.7,
        pitch: 0,
        bearing: 0,
        hash: true
      });

      map.on("load", function () {
        const activeLayerIds = buildingLayerIds.length ? buildingLayerIds : interactiveLayerIds;

        function pickFeature(point) {
          if (activeLayerIds.length) {
            const preferredLayers = buildingLayerIds.length ? buildingLayerIds : activeLayerIds;
            const preferred = map.queryRenderedFeatures(point, { layers: preferredLayers });
            if (preferred.length) {
              return preferred[0];
            }
          }

          const fallback = map.queryRenderedFeatures(point);
          return fallback.find(function (feature) {
            return feature && feature.properties && Object.keys(feature.properties).length > 0;
          }) || fallback[0] || null;
        }

        activeLayerIds.forEach(function (layerId) {
          map.on("mouseenter", layerId, function () {
            map.getCanvas().style.cursor = "pointer";
          });
          map.on("mouseleave", layerId, function () {
            map.getCanvas().style.cursor = "";
          });
        });

        map.on("click", function (event) {
          const feature = pickFeature(event.point);
          if (!feature) {
            showNoFeature("No building feature found here. Click on a building footprint to inspect its OSM tags.");
            return;
          }
          renderFeature(feature);
        });

        if (cfg.tile && cfg.tile.has) {
          showNoFeature("Click a building footprint to inspect its OSM tags.");
        } else {
          showNoFeature("Showing the current basemap. Add /preview/{z}/{x}/{y} to focus a specific tile.");
        }

        if (!cfg.tile || !cfg.tile.has) {
          return;
        }
        map.fitBounds(
          [
            [cfg.tile.bounds[0], cfg.tile.bounds[1]],
            [cfg.tile.bounds[2], cfg.tile.bounds[3]]
          ],
          { padding: 48, duration: 0, linear: true, maxZoom: 22 }
        );
      });
    })().catch(function (err) {
      const panel = document.querySelector(".panel");
      panel.insertAdjacentHTML(
        "beforeend",
        "<p class=\"note\" style=\"color:#fca5a5\">Preview error: " + String(err && err.message ? err.message : err) + "</p>"
      );
      throw err;
    });
  </script>
</body>
</html>`

	return []byte(strings.ReplaceAll(previewHTML, "__BASEMAP_PREVIEW_DATA__", string(pageJSON))), nil
}

func jsonForPreview(data previewPageData) ([]byte, error) {
	type previewTileJSON struct {
		Z      int       `json:"z"`
		X      int       `json:"x"`
		Y      int       `json:"y"`
		Has    bool      `json:"has"`
		Bounds []float64 `json:"bounds,omitempty"`
	}
	type previewPayload struct {
		TileJSONURL string          `json:"tilejson_url"`
		RawURL      string          `json:"raw_url"`
		APIKey      string          `json:"apiKey"`
		Tile        previewTileJSON `json:"tile"`
	}

	payload := previewPayload{
		TileJSONURL: data.TileJSONURL,
		RawURL:      data.Tile.RawURL,
		APIKey:      data.APIKey,
		Tile: previewTileJSON{
			Z:   data.Tile.Z,
			X:   data.Tile.X,
			Y:   data.Tile.Y,
			Has: data.Tile.Has,
		},
	}
	if data.Tile.Has {
		payload.Tile.Bounds = []float64{data.Tile.Box[0], data.Tile.Box[1], data.Tile.Box[2], data.Tile.Box[3]}
	}

	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return b, nil
}
