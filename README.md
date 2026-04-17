# SpotiFLACAPI

REST API wrapper for [SpotiFLAC](https://github.com/afkarxyz/SpotiFLAC) focused on a simple use case:

1. Receive a Spotify track URL/ID.
2. Resolve and download audio using SpotiFLAC providers.
3. Return a browser-friendly download URL.

## Features

- Spotify track input (`https://open.spotify.com/track/...`, `spotify:track:...`, or raw track ID).
- Provider fallback chain (default order: `tidal -> qobuz -> amazon`).
- Download engine selector: `auto`, `spotiflac`, or `monochrome`.
- Temporary tokenized download URLs (`GET /v1/download/{token}`).
- In-memory token store with TTL-based cleanup.
- CORS enabled (`*`) for frontend/API integrations.
- Automatic `ffmpeg/ffprobe` bootstrap on first download request (enabled by default).

## Architecture

- `POST /v1/download-url`
  - Validates input, provider order, and download engine.
  - Ensures `ffmpeg/ffprobe` are available (auto-install if missing and enabled).
  - Fetches Spotify metadata through SpotiFLAC backend.
  - Uses `spotiflac`, `monochrome`, or `auto` fallback mode.
  - Stores file path + token + expiry in memory.
  - Returns a public download URL.
- `GET /v1/download/{token}`
  - Validates token and expiry.
  - Streams file with `Content-Disposition: attachment`.
- Background cleaner
  - Removes expired tokens and associated temp files.

## Requirements

- Go `1.25+`
- Network access to upstream services used by SpotiFLAC.

## Run

Default local run:

```bash
go run .
```

Run on `127.0.0.1:9000`:

```bash
BIND_ADDR=127.0.0.1 PORT=9000 BASE_URL=http://127.0.0.1:9000 go run .
```

## Configuration

- `BIND_ADDR`: bind address for the HTTP server (default: `127.0.0.1`)
- `PORT`: HTTP port (default: `8080`)
- `DOWNLOAD_TTL`: token TTL as Go duration (default: `2h`, example: `30m`)
- `BASE_URL`: optional public base URL used when building `download_url`
- `HTTP_CLIENT_TIMEOUT`: timeout for outbound HTTP calls from this API (default: `20s`)
- `FFMPEG_AUTO_INSTALL`: auto-install `ffmpeg/ffprobe` when missing (default: `true`)
  - accepted true values: `1`, `true`, `yes`, `on`
  - accepted false values: `0`, `false`, `no`, `off`
- `SPOTIFY_METADATA_TIMEOUT`: timeout for Spotify metadata resolution through SpotiFLAC (default: `45s`)
- `MONOCHROME_DISCOVERY_URLS`: optional comma-separated list of discovery endpoints that return current Monochrome instances
- `MONOCHROME_API_INSTANCES`: optional comma-separated override for Monochrome search/info instances
- `MONOCHROME_STREAMING_INSTANCES`: optional comma-separated override for Monochrome streaming/manifest instances
- `MONOCHROME_TIDAL_CLIENT_ID`: optional override for the browser client id used by Monochrome
- `MONOCHROME_TIDAL_CLIENT_SECRET`: optional override for the browser client secret used by Monochrome

Defaults are kept in code. For day-to-day maintenance, the env surface is intentionally limited to what is most likely to change operationally: discovery URLs, Monochrome instance lists, and optional TIDAL browser credentials.

Official TIDAL URLs and Monochrome endpoint paths are fixed in code on purpose. If those ever change, that is a code update rather than an `.env` update.

There is also a ready-to-edit example file in `/Users/mariano.palomo/Dev/SpotiFLACAPI/.env.example`.

Example:

```bash
BIND_ADDR=127.0.0.1 PORT=9000 DOWNLOAD_TTL=1h BASE_URL=http://127.0.0.1:9000 go run .
```

## FFmpeg behavior

SpotiFLAC backend uses `ffmpeg` and/or `ffprobe` in several download and metadata paths.

In this API:

- `ffmpeg/ffprobe` are checked before processing download requests.
- If missing and `FFMPEG_AUTO_INSTALL=true`, the API downloads and installs them automatically.
- If auto-install is disabled and binaries are missing, requests fail with a clear error.

## API

### `GET /health`

Returns service status and current UTC timestamp.

### `POST /v1/download-url`

Creates a downloadable resource from a Spotify track.

Request body:

```json
{
  "spotify_url": "https://open.spotify.com/track/3n3Ppam7vgaVa1iaRUc9Lp",
  "services": ["tidal", "qobuz", "amazon"],
  "ttl_seconds": 3600,
  "engine": "auto"
}
```

Notes:

- `spotify_url` is required.
- `services` is optional; default order is `["tidal", "qobuz", "amazon"]`.
- `ttl_seconds` is optional and capped server-side.
- `engine` is optional; valid values are `auto`, `spotiflac`, and `monochrome`.
- `method` is accepted as an alias of `engine` in the JSON body.
- `?engine=...` and `?method=...` in the request URL override the JSON body and are useful to force a mode during testing.

Success response:

```json
{
  "ok": true,
  "spotify_id": "3n3Ppam7vgaVa1iaRUc9Lp",
  "service": "tidal",
  "method": "spotiflac",
  "filename": "Track - Artist.flac",
  "download_url": "http://127.0.0.1:9000/v1/download/<token>",
  "expires_at": "2026-02-21T12:00:00Z",
  "attempts": [
    {
      "service": "tidal"
    }
  ]
}
```

Failure response:

```json
{
  "ok": false,
  "error": "failed in all services: tidal -> qobuz -> amazon",
  "attempts": [
    {
      "service": "tidal",
      "error": "..."
    },
    {
      "service": "qobuz",
      "error": "..."
    },
    {
      "service": "amazon",
      "error": "..."
    }
  ]
}
```

### `GET /v1/download/{token}`

Returns the audio file as an attachment if token is valid and not expired.

## Usage example

```bash
curl -s -X POST http://127.0.0.1:9000/v1/download-url \
  -H 'Content-Type: application/json' \
  -d '{"spotify_url":"https://open.spotify.com/track/3n3Ppam7vgaVa1iaRUc9Lp"}'
```

Force Monochrome for testing:

```bash
curl -s -X POST 'http://127.0.0.1:9000/v1/download-url?engine=monochrome' \
  -H 'Content-Type: application/json' \
  -d '{"spotify_url":"https://open.spotify.com/track/3n3Ppam7vgaVa1iaRUc9Lp"}'
```

Force custom Monochrome settings from environment:

```bash
MONOCHROME_DISCOVERY_URLS=https://example-worker-1.dev,https://example-worker-2.dev \
MONOCHROME_API_INSTANCES=https://api-a.example,https://api-b.example \
MONOCHROME_STREAMING_INSTANCES=https://stream-a.example,https://stream-b.example \
go run .
```

Then open the returned `download_url` in a browser or fetch it with:

```bash
curl -L -o track.flac "http://127.0.0.1:9000/v1/download/<token>"
```

## Dependency management (SpotiFLAC upstream)

SpotiFLAC now uses `module github.com/afkarxyz/SpotiFLAC`, so this project pins upstream directly as a normal module requirement in `go.mod`.

Current pin:

- Commit: `7346730be98c`
- Pseudo-version: `v0.0.0-20260414003641-7346730be98c`

Update to latest upstream commit:

```bash
./scripts/pin-upstream.sh latest
```

Or pin a specific pseudo-version:

```bash
./scripts/pin-upstream.sh v0.0.0-20260414003641-7346730be98c
```

After changing upstream:

```bash
go build ./...
go test ./...
```
