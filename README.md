# SpotiFLACAPI

REST API wrapper for [SpotiFLAC](https://github.com/afkarxyz/SpotiFLAC) focused on a simple use case:

1. Receive a Spotify track URL/ID.
2. Resolve and download audio using SpotiFLAC providers.
3. Return a browser-friendly download URL.

## Features

- Spotify track input (`https://open.spotify.com/track/...`, `spotify:track:...`, or raw track ID).
- Provider fallback chain (default order: `tidal -> qobuz -> amazon`).
- Temporary tokenized download URLs (`GET /v1/download/{token}`).
- In-memory token store with TTL-based cleanup.
- CORS enabled (`*`) for frontend/API integrations.
- Automatic `ffmpeg/ffprobe` bootstrap on first download request (enabled by default).

## Architecture

- `POST /v1/download-url`
  - Validates input and provider order.
  - Ensures `ffmpeg/ffprobe` are available (auto-install if missing and enabled).
  - Fetches Spotify metadata through SpotiFLAC backend.
  - Tries providers in order until one succeeds.
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
- `FFMPEG_AUTO_INSTALL`: auto-install `ffmpeg/ffprobe` when missing (default: `true`)
  - accepted true values: `1`, `true`, `yes`, `on`
  - accepted false values: `0`, `false`, `no`, `off`

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
  "ttl_seconds": 3600
}
```

Notes:

- `spotify_url` is required.
- `services` is optional; default order is `["tidal", "qobuz", "amazon"]`.
- `ttl_seconds` is optional and capped server-side.

Success response:

```json
{
  "ok": true,
  "spotify_id": "3n3Ppam7vgaVa1iaRUc9Lp",
  "service": "tidal",
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

Then open the returned `download_url` in a browser or fetch it with:

```bash
curl -L -o track.flac "http://127.0.0.1:9000/v1/download/<token>"
```

## Dependency management (SpotiFLAC upstream)

SpotiFLAC currently uses `module spotiflac`, so this project pins upstream using `replace` in `go.mod`.

Current pin:

- Commit: `1314c14c592f79058823b7fa99bd92c4f1922ac5`
- Pseudo-version: `v0.0.0-20260212123831-1314c14c592f`

Update to latest upstream commit:

```bash
./scripts/pin-upstream.sh latest
```

Or pin a specific pseudo-version:

```bash
./scripts/pin-upstream.sh v0.0.0-20260212123831-1314c14c592f
```

After changing upstream:

```bash
go build ./...
go test ./...
```
