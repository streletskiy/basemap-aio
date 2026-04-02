# basemap-aio

Compose stack for downloading the latest Protomaps basemap build, keeping only the current archive on disk, and serving it as:

- raw `.pmtiles` files for PMTiles-aware clients
- PMTiles ZXY endpoints and TileJSON
- an optional `?key=` or `X-API-Key` protected gateway

## What it does

- Pulls the newest daily build metadata from Protomaps.
- Downloads the corresponding `pmtiles` archive from the official build bucket.
- Verifies the archive with the published hash when available.
- Exposes a stable `current` alias:
  - `/current.pmtiles`
  - `/current.json`
  - `/current/{z}/{x}/{y}.mvt`
- Keeps the latest build updated on a timer.
- Supports manual refresh with a one-shot command.
- Prints download progress, speed and ETA to container logs while fetching the archive.

The Protomaps docs say the basemap daily builds are available from `maps.protomaps.com/builds`, and `pmtiles serve` is the supported way to expose ZXY and TileJSON endpoints. This stack uses that model, but adds a small proxy so the current build can move without changing client URLs.

## Files

- `docker-compose.yml` - full stack
- `Dockerfile` - updater/proxy image
- `cmd/basemapctl` and `internal/basemap` - updater and proxy logic
- `.env.example` - local overrides
- `.gitignore` and `.dockerignore` - local data and build-context ignores

## Quick start

1. Copy `.env.example` to `.env` and adjust if needed.
2. Start the stack:

```bash
docker compose up -d
```

3. Wait for the first update to finish, then use:

```text
http://localhost:8080/status
http://localhost:8080/current.json
http://localhost:8080/current.pmtiles
```
## Manual update

Run a one-shot refresh against the same shared volume:

```bash
docker compose run --rm updater update
```

Force a redownload of the latest build:

```bash
docker compose run --rm updater update --force
```

## Scheduled updates

The `updater` service runs continuously and retries every `UPDATE_INTERVAL`:

```env
UPDATE_INTERVAL=24h
```

Other useful env vars:

- `API_KEY`
- `PUBLIC_URL`
- `DATA_HOST_DIR`
- `DATA_DIR`
- `METADATA_URL`
- `DOWNLOAD_BASE_URL`

If you want a different cadence, change that value and restart the stack.

By default the archives are stored in `./data` on the host and mounted to `/data` in the containers.

## API key mode

If `API_KEY` is set, the proxy requires either:

- `?key=YOUR_KEY`
- `X-API-Key: YOUR_KEY`

The proxy rewrites TileJSON so the returned tile URLs keep working with the same key.

Example:

```env
API_KEY=secret-token
```

Then load:

```text
http://localhost:8080/current.json?key=secret-token
```

## Client usage

Use `/current.json` when the client understands TileJSON, for example MapLibre GL JS:

```js
const source = {
  type: "vector",
  url: "http://localhost:8080/current.json"
}
```

If `API_KEY` is enabled, append it to the TileJSON request so the proxy can rewrite the tile URLs with the same key:

```js
const source = {
  type: "vector",
  url: "http://localhost:8080/current.json?key=secret-token"
}
```

For MapLibre GL JS, the key normally goes on the TileJSON URL only. The proxy then injects the same key into the returned tile URLs, so the client does not need to add it to every tile request manually.

Equivalent request forms:

- without key: `GET /current.json`
- with key: `GET /current.json?key=YOUR_KEY`
- with header auth: `GET /current.json` plus `X-API-Key: YOUR_KEY`

For raw PMTiles-aware clients, use:

- without key: `GET /current.pmtiles`
- with key: `GET /current.pmtiles?key=YOUR_KEY`
- with header auth: `GET /current.pmtiles` plus `X-API-Key: YOUR_KEY`

The proxy also exposes the current versioned path, but only the latest archive is kept on disk, so older versioned URLs are not retained after the next refresh.

## Notes

- First download can be large and needs enough free disk for one temporary copy during the update.
- The stack keeps only the latest archive by default.
- The `tiles` service uses `protomaps/go-pmtiles:v1.30.1`. If you want a different server version, change that image tag in `docker-compose.yml`.
