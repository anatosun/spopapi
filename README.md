# spopapi

`spopapi` is a Go proxy for the Spotify Web API. It uses Redis for caching and Postgres for permanent storage. Catalog data can be imported from the Spotify metadata dump. Missing data is fetched from Spotify and saved in Postgres.

The server listens on port 8080 and accepts the same GET paths as `https://api.spotify.com`.

## How requests work

For each `GET /v1/*` request, the server checks:

1. Redis
2. The Postgres `responses` table
3. The imported catalog tables in Postgres
4. The Spotify Web API

Responses fetched from Spotify are saved in Postgres with `source='upstream'`. Responses built from imported data are saved with `source='dump'`. Both are copied to Redis when requested.

Simultaneous requests for the same missing resource share one fetch.

### Responses built from imported data

The following requests can be answered directly from the catalog tables:

```text
GET /v1/tracks/{id}
GET /v1/albums/{id}
GET /v1/artists/{id}
GET /v1/audio-features/{id}
```

Album responses include the first page of tracks. Audio features are available from the dump even for Spotify apps that cannot access the deprecated upstream endpoint.

Other requests go to Spotify. This includes search, playlists, requests with query parameters, and endpoints that request multiple IDs. Their responses are still cached and stored.

### Cache expiration

| Data | Redis TTL |
| --- | --- |
| Tracks, albums, and artists | 7 days |
| Search results | 1 hour |
| Playlists, top tracks, and recommendations | 10 minutes |
| Other responses | 1 hour |
| 404 responses | 5 minutes |

## Setup

Start Redis and Postgres:

```bash
docker compose up -d redis postgres
```

Create `.env` if it does not exist:

```bash
cp .env.example .env
```

Set your Spotify application credentials and a separate token for proxy clients in `.env`:

```dotenv
SPOTIFY_CLIENT_ID=your_client_id
SPOTIFY_CLIENT_SECRET=your_client_secret
PROXY_API_TOKEN=your_proxy_token
```

Generate a random proxy token with:

```bash
openssl rand -hex 32
```

The server refuses to start without `PROXY_API_TOKEN`. Every endpoint except `/healthz`, including `/stats`, requires this bearer token. It is separate from the Spotify credentials used for upstream requests.

Build and start the server:

```bash
go build ./...
set -a; source .env; set +a
go run ./cmd/server
```

The server reads environment variables. It does not load `.env` itself, so the `source` command is needed when starting it from a shell.

Example requests:

```bash
curl -D- \
  -H "Authorization: Bearer $PROXY_API_TOKEN" \
  http://127.0.0.1:8080/v1/tracks/4uLU6hMCjMI75M1A2tKUQC

curl -H "Authorization: Bearer $PROXY_API_TOKEN" \
  http://127.0.0.1:8080/stats

curl http://127.0.0.1:8080/healthz
```

`X-Cache: HIT` means the response came from Redis or Postgres. `X-Cache: MISS` means the server called Spotify.

## Run the proxy with Docker Compose

After setting the credentials and proxy token in `.env`, build and start the proxy container:

```bash
docker compose up -d --build proxy
```

The proxy port is published only on `127.0.0.1`. Inside the container network, it connects to `redis:6379` and `postgres:5432`.

To use an image published by GitHub Actions instead of building locally:

```bash
export SPOPAPI_IMAGE=ghcr.io/OWNER/spopapi:latest
docker compose pull proxy
docker compose up -d --no-build proxy
```

Replace `OWNER` with the GitHub user or organization that owns the repository.

The workflow in `.github/workflows/docker.yml` runs the tests and publishes `linux/amd64` and `linux/arm64` images to GHCR. It runs for pushes to `main`, version tags, and manual workflow runs. The `main` branch publishes `latest`; version tags publish their corresponding version.

## Import the metadata dump

Build the importer:

```bash
go build -o spopapi-import ./cmd/importer
```

Set `INGEST_DIR` to the folder containing the dump files. You can add it to `.env` or export it in the shell:

```bash
export INGEST_DIR=/path/to/annas_archive_spotify_2025_07_metadata
```

The importer expects these filenames in that folder:

```text
spotify_clean.sqlite3.zst
spotify_clean_audio_features.sqlite3.zst
```

Import tracks, albums, artists, and related tables:

```bash
./spopapi-import -kind core
```

Import audio features after the core import finishes:

```bash
./spopapi-import -kind audio-features
```

`INGEST_DIR` is also the default decompression directory. Compressed input is decompressed to a temporary SQLite file, which is removed when the importer exits. Use `-temp-dir` to select a different directory.

The importer also accepts a plain `.sqlite3` file through `-dump`. An explicit `-dump` path overrides the standard path derived from `INGEST_DIR`.

Allow about 130 GB of free space for the core database and 45 GB for the audio-features database. Only one file needs to be decompressed at a time.

Rows are read in batches and written with Postgres `COPY`. The default batch size is 50,000 rows. Change it with `-batch` if needed.

### Resume an import

The `import_state` table records completed tables. If the importer is run again, completed tables are skipped. If a table was only partly imported, that table is truncated and imported again.

To rebuild tables even when they are marked complete, use `-fresh`.

Redis is not filled during import. Entries are added when the API receives requests.

### Dump files not imported

Playlists, shows, episodes, and audiobooks are not imported by the current importer. Requests for these resources use the Spotify fallback.

The `track_files` database is not used. It maps tracks to archived audio files rather than Spotify API metadata.

## Tests

Run the standard checks:

```bash
go vet ./...
go build ./...
go test ./...
```

Run the integration test against the local Postgres container:

```bash
TEST_DATABASE_URL='postgres://spopapi:spopapi@localhost:5432/spopapi' go test ./...
```

The integration test creates a small SQLite database with the dump schema, compresses it with zstd, and imports it into a temporary Postgres schema. It then checks response generation and import resumption. The temporary schema is dropped after the test. The real dump is not accessed.

## Limitations

The server uses the Spotify client-credentials flow. It does not implement user OAuth, so endpoints that require a user access token, such as `/me` and playback endpoints, are not supported.

This setup is intended for personal use on one machine. Keep it bound to localhost and do not use it to redistribute the dump.
