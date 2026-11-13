<p align="center">
  <img src="https://raw.githubusercontent.com/jmoruno/plexmor/main/internal/panel/static/logo.png" width="128" height="128" alt="Plexmor">
</p>

<h1 align="center">Plexmor</h1>

<p align="center"><em>Your Plex and the ones your friends share with you, inside Stremio.</em></p>

<p align="center">
  <a href="https://github.com/jmoruno/plexmor/actions/workflows/ci.yml"><img src="https://github.com/jmoruno/plexmor/actions/workflows/ci.yml/badge.svg?branch=main" alt="CI"></a>
  <a href="https://github.com/jmoruno/plexmor/actions/workflows/docker.yml"><img src="https://github.com/jmoruno/plexmor/actions/workflows/docker.yml/badge.svg?branch=main" alt="Build"></a>
  <a href="https://github.com/jmoruno/plexmor/actions/workflows/codeql.yml"><img src="https://github.com/jmoruno/plexmor/actions/workflows/codeql.yml/badge.svg?branch=main" alt="CodeQL"></a>
  <a href="https://github.com/jmoruno/plexmor/actions/workflows/trivy.yml"><img src="https://github.com/jmoruno/plexmor/actions/workflows/trivy.yml/badge.svg?branch=main" alt="Trivy"></a>
  <a href="https://github.com/jmoruno/plexmor/releases/latest"><img src="https://img.shields.io/github/v/release/jmoruno/plexmor" alt="Release"></a>
  <a href="https://hub.docker.com/r/jmorunofdz/plexmor"><img src="https://img.shields.io/docker/pulls/jmorunofdz/plexmor" alt="Docker pulls"></a>
  <a href="https://hub.docker.com/r/jmorunofdz/plexmor"><img src="https://img.shields.io/docker/image-size/jmorunofdz/plexmor/stable" alt="Image size"></a>
  <a href="https://github.com/jmoruno/plexmor/blob/main/LICENSE"><img src="https://img.shields.io/github/license/jmoruno/plexmor" alt="License"></a>
  <br>
  <a href="https://buymeacoffee.com/jmoruno"><img src="https://img.shields.io/badge/Buy%20me%20a%20coffee-FFDD00?logo=buymeacoffee&logoColor=black" alt="Buy Me a Coffee"></a>
</p>

<p align="center"><a href="https://github.com/jmoruno/plexmor/blob/main/README.md">Español</a> · <b>English</b></p>

Stremio addon (works with Nuvio and AIOStreams) for your Plex server and the ones your friends share with you, with a web panel and multiple users.
It answers from its own in-memory index, so Stremio never waits for a Plex server.

- **Sources:** every version on every server, sorted by language (Spain and Latin American Spanish apart), resolution, HDR and latency, with codec, audio tracks, subtitles and size.
- **Away from home:** in automatic mode, when a server can only be reached from your network the video goes through Plexmor and your Plex token never leaves home. There are also "always through Plexmor" and "straight to Plex" modes.
- **Catalogs:** recently added, continue watching, Plex watchlist, every library (with a genre filter) and search across all servers.
- **External subtitles** and optional **lighter versions** (1080p and 720p transcoded by Plex).
- **Web panel in English or Spanish:** sign in with Plex, install in Stremio, languages, qualities, libraries and users.
- **Multiple users:** anyone with access to your Plex can sign in; each person gets their own address, preferences and access to each server.
- **Metrics** at `/metrics` (Prometheus format).

## Language

| Variable | Default | Use |
| --- | --- | --- |
| `LANGUAGE` | `es` | Language of the panel and of the texts in Stremio: `es` or `en`. Each user can change it in the panel |
| `LANGUAGES` | follows `LANGUAGE` | Default audio languages, best first: `castellano,latino,ingles` with `es` and `ingles` with `en`. English names work too (`english`, `japanese`, `spanish`…) |

For an English setup, set `LANGUAGE=en`.

## Install (Portainer or docker compose)

1. A stack with [`docker-compose.yml`](docker-compose.yml) (image `jmorunofdz/plexmor:stable`).
2. Stack variables: those in [`.env.example`](.env.example). The minimum is `DATA_PATH` and `PLEX_CONFIG`.
3. Open the panel (`http://<server>:7254/` or your domain), sign in with Plex and press **Install in the app** or **Open in Stremio Web**.

To use it away from home, publish it over HTTPS (for example with Nginx Proxy Manager). Stremio Web requires HTTPS.

| Variable | Default | What it is |
| --- | --- | --- |
| `DATA_PATH` | `./data` | Host folder for the index, the accounts and the secret |
| `PLEX_CONFIG` | — | Plex folder that holds `Preferences.xml` (read-only). The server owner becomes the administrator without typing any token |
| `PLEX_TOKEN` | — | Instead of `PLEX_CONFIG` when Plex runs on another machine. Without either, the first person to sign in to the panel is the administrator |
| `PUBLIC_URL` | — | Public address (`https://plexmor.example.com`). Taken from each request when empty |
| `PLEXMOR_SECRET` | random | Encrypts the stored tokens and signs the sessions. When empty one is created in `/data/secret` |
| `METRICS_TOKEN` | — | Lets Prometheus read `/metrics` with `Authorization: Bearer <token>`. Without it, only panel administrators can |
| `PLEXMOR_PORT` | `7254` | Published port |
| `SERVERS` | all | Only use these servers (by name) |
| `PUID` / `PGID` | `99` / `100` | Container user (Unraid's default) |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |

Others, for specific cases: `SYNC_INTERVAL` (10m), `HEALTH_INTERVAL` (1m), `RESOURCES_INTERVAL` (30m), `PLEX_TIMEOUT` (20s), `MAX_STREAMS`, `PLEXMOR_KEY` (administrator address), `TRAKT_CLIENT_ID` / `TRAKT_CLIENT_SECRET` (Trakt module, VIP only).

## Image and versions

| Tag | Channel | Use |
| --- | --- | --- |
| `stable` / `latest` | stable | Production: latest stable version |
| `X.Y` / `X.Y.Z` (e.g. `0.3.0`) | stable | Pin a version or roll back |
| `beta` / `X.Y.Z-rc.N` | beta | Release candidates, before they become stable |
| `main` and other branches | development | Try changes without touching production |
| `sha-<commit>` | — | One exact build |

Images for `linux/amd64` and `linux/arm64`, with provenance and SBOM.

Workflow:

1. Changes are made on a branch. Every push publishes `:<branch>`.
2. When it works, it is merged into `main`.
3. To publish a version: **Actions → Imagen Docker → Run workflow** on `main` with the version number (it needs its section in [CHANGELOG.md](CHANGELOG.md)).
   - **Stable** (`0.3.0`): GitHub release and `:0.3.0`, `:0.3`, `:stable` and `:latest`.
   - **Beta** (`0.3.0-rc.1`): GitHub pre-release and `:0.3.0-rc.1` and `:beta`.

## Security

- Each user's address carries a random key: do not share it. **Generate a new address** in the panel revokes the old one.
- Plex tokens are stored encrypted and never logged.
- Panel with signed sessions, CSRF protection, a strict Content-Security-Policy and no third-party resources.
- Distroless image without a shell, non-root user, read-only filesystem and no capabilities.
- Scanned with govulncheck, CodeQL and Trivy.

Reporting a security issue: [SECURITY.md](SECURITY.md).

## Development

```sh
go test -race ./...                      # tests (fake Plex, plex.tv and Trakt, end to end)
go test -run '^$' -bench . ./...         # /stream performance
DATA_DIR=./data go run ./cmd/plexmor      # panel at http://localhost:7254
```

Standard library only: no external dependencies.

## License

[MIT](LICENSE): you can use, copy, modify and redistribute it freely, also commercially, as long as the copyright notice is kept. The panel fonts (Bricolage Grotesque and Instrument Sans) are under the [SIL OFL 1.1](internal/panel/static/fonts).

## Disclaimer

This software is provided **as is**, without warranty of any kind (see [LICENSE](LICENSE)). The author is not liable for any damage, loss or claim arising from its use.

- **It does not include, host or distribute any content.** It is a technical tool that only connects to the Plex servers each user already has access to.
- **Each user is solely responsible** for how they use it and for complying with applicable laws (including intellectual property) and the terms of the services they connect to.
- **Not affiliated** with or endorsed by Plex, Stremio, Nuvio, AIOStreams, Trakt or any other project or brand mentioned. Names belong to their respective owners.

## Support

If you find it useful, you can buy me a coffee: [buymeacoffee.com/jmoruno](https://buymeacoffee.com/jmoruno) ☕
