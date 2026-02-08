<h1 align="center">
  <img src="docs/media/conops.png" alt="ConOps Logo" width="200">
  <br>
  ConOps
</h1>

<p align="center">
  <strong>GitOps for Docker Compose. Like Argo CD, but without Kubernetes.</strong>
</p>

<p align="center">
  Point ConOps at a Git repo containing a <code>docker-compose.yaml</code>.<br>
  It clones, pulls, deploys, watches for new commits, detects container drift, and self-heals.<br>
  One binary. No cluster required.
</p>

<p align="center">
  <a href="#quick-start">Quick Start</a> &middot;
  <a href="#features">Features</a> &middot;
  <a href="#how-it-works">How It Works</a> &middot;
  <a href="#configuration">Configuration</a> &middot;
  <a href="#api-reference">API</a>
</p>

---

  ![ConOps Demo](docs/media/conops-demo.gif)


## The Problem

You're running services with Docker Compose. Deployments are SSH-and-pray. You `git pull` on the server, run `docker compose up -d`, and hope nothing drifted while you weren't looking. There's no audit trail, no automatic rollout on push, and no visibility into what's actually running vs. what's in Git.

Kubernetes has Argo CD and Flux for this. Docker Compose has had nothing. Until now.

## What ConOps Does

ConOps is a single controller that continuously reconciles your Docker Compose applications against their Git source of truth.

1. **You register an app** &mdash; a Git repo, a branch, and a path to a compose file.
2. **ConOps polls the branch** &mdash; when it sees a new commit, it marks the app as pending.
3. **The reconciler kicks in** &mdash; it runs Docker preflight, then clones/fetches the repo, runs `docker compose pull`, and runs `docker compose up -d --remove-orphans`.
4. **Drift detection runs continuously** &mdash; if a container crashes, exits, or goes unhealthy, ConOps re-deploys automatically.

You get a web dashboard for visibility and a REST API for automation.

## Features

- **Git-driven deployments** &mdash; push to your branch, ConOps handles the rest
- **Continuous reconciliation** &mdash; configurable loop that keeps desired state in sync
- **Self-healing** &mdash; detects missing, exited, or unhealthy containers and recovers
- **Docker preflight + fallback toolchain** &mdash; checks Docker API compatibility before sync and auto-installs compatible tooling when needed
- **Web UI** &mdash; register apps, inspect status, view containers, trigger syncs, read logs
- **REST API + CLI** &mdash; automate everything; nothing in the UI that the API can't do
- **Private repo support** &mdash; GitHub deploy keys with AES-GCM encryption at rest
- **SQLite or PostgreSQL** &mdash; SQLite for single-node, Postgres for production
- **Single binary** &mdash; no runtime dependencies beyond Docker and Git
- **Multi-arch Docker image** &mdash; `linux/amd64` and `linux/arm64`

## Quick Start

### Docker (recommended)

```bash
docker run \
  --name conops \
  -p 8080:8080 \
  -e CONOPS_RUNTIME_DIR=/tmp/conops-runtime \
  -v /tmp/conops-runtime:/tmp/conops-runtime \
  -v conops_data:/data \
  -v /var/run/docker.sock:/var/run/docker.sock \
  anurag1201/conops:latest
```

This uses SQLite by default. SQLite is fine for daily use, but we recommend
PostgreSQL for production. You can pass the Postgres environment variables
(`DB_TYPE=postgres` and `DB_CONNECTION_STRING=postgres://...`) to ConOps to
enable that.

`CONOPS_RUNTIME_DIR` is important when ConOps runs in Docker and talks to the
host daemon through `/var/run/docker.sock`. Use a host bind-mounted absolute
path so Compose bind mounts and file-based secrets resolve correctly.

ConOps runs Docker preflight before each sync. If the system Docker client is
too old, ConOps can auto-install a compatible Docker CLI and Compose plugin.

Open **http://localhost:8080** and register your first app.

### Register your first app

From the UI, click **New App**. Or use the API:

```bash
curl -X POST http://localhost:8080/api/v1/apps/ \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Example Application",
    "repo_url": "https://github.com/docker/awesome-compose",
    "repo_auth_method": "public",
    "branch": "master",
    "compose_path": "fastapi/compose.yaml",
    "poll_interval": "30s"
  }'
```

Push a commit to the tracked branch and watch ConOps pick it up and deploy.

## How It Works

```
                    ┌─────────────┐
                    │  Git Repo   │
                    └──────┬──────┘
                           │ poll
                    ┌──────▼──────┐
                    │ Git Watcher │──── new commit? ──→ mark pending
                    └─────────────┘
                           │
                    ┌──────▼──────┐
                    │ Reconciler  │──── clone/fetch → compose pull → compose up
                    └──────┬──────┘
                           │
                    ┌──────▼──────┐
                    │Drift Checker│──── container missing/exited/unhealthy? → requeue
                    └─────────────┘
```

**Status flow:**

`registered` &rarr; `pending` &rarr; `syncing` &rarr; `synced`

If something goes wrong: `syncing` &rarr; `error` (retryable).

If a container drifts: `synced` &rarr; `pending` &rarr; re-reconcile.

ConOps separates **change detection** (Git watcher) from **state application** (reconciler). This keeps the control loop predictable and easy to reason about.

## Private Repositories

ConOps supports private GitHub repos via SSH deploy keys.

```bash
curl -X POST http://localhost:8080/api/v1/apps/ \
  -H "Content-Type: application/json" \
  -d '{
    "name": "my-private-app",
    "repo_url": "git@github.com:your-org/private-repo.git",
    "repo_auth_method": "deploy_key",
    "deploy_key": "-----BEGIN OPENSSH PRIVATE KEY-----\n...\n-----END OPENSSH PRIVATE KEY-----",
    "branch": "main",
    "compose_path": "docker-compose.yml"
  }'
```

Deploy keys are encrypted at rest using AES-GCM. ConOps auto-generates an encryption key on first run, or you can provide your own via `CONOPS_ENCRYPTION_KEY`. Strict SSH host verification is enforced.

## Configuration

All configuration is via environment variables.

| Variable | Default | Description |
|----------|---------|-------------|
| `DB_TYPE` | `sqlite` | Storage backend: `sqlite` or `postgres` |
| `DB_CONNECTION_STRING` | &mdash; | Required when using `postgres` |
| `CONOPS_RECONCILE_INTERVAL` | `10s` | How often the reconciler runs |
| `CONOPS_SYNC_TIMEOUT` | `5m` | Max duration for a single sync operation |
| `CONOPS_RETRY_ERRORS` | `false` | Auto-retry apps that entered `error` status |
| `CONOPS_RUNTIME_DIR` | `./.conops-runtime` | Runtime checkout directory used for compose execution |
| `CONOPS_TOOLS_DIR` | `./.conops-tools` | Cache directory for managed Docker CLI and Compose plugin downloads |
| `CONOPS_DOCKER_CLI_PATH` | &mdash; | Advanced: absolute path to a pre-installed Docker CLI binary |
| `CONOPS_DOCKER_CLI_VERSION` | &mdash; | Advanced: pin managed Docker CLI version (for example `29.2.1`) |
| `CONOPS_COMPOSE_PLUGIN_VERSION` | &mdash; | Advanced: pin managed Compose plugin version (for example `v5.0.2`) |
| `CONOPS_ENCRYPTION_KEY` | &mdash; | 32-byte key (raw or base64) for deploy key encryption |
| `CONOPS_ENCRYPTION_KEY_FILE` | `/data/conops-encryption.key` | Path to read/write the encryption key |
| `CONOPS_KNOWN_HOSTS_FILE` | auto | SSH known_hosts file for host verification |

## API Reference

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/` | Redirects to dashboard |
| `GET` | `/ui/apps` | Web dashboard |
| `GET` | `/ui/apps/new` | App registration form |
| `GET` | `/ui/apps/{id}` | App detail view |
| `GET` | `/api/v1/apps/` | List all apps |
| `POST` | `/api/v1/apps/` | Register a new app |
| `GET` | `/api/v1/apps/{id}` | Get app details |
| `POST` | `/api/v1/apps/{id}/sync` | Force sync |
| `DELETE` | `/api/v1/apps/{id}` | Delete app and its resources |

### CLI

```bash
./bin/conops-ctl apps list              # List all apps
./bin/conops-ctl apps add app.json      # Register from JSON file
./bin/conops-ctl apps delete <app-id>   # Delete an app
```

## Who Is This For

- **Homelab operators** who want git push deploys without Kubernetes
- **Small teams** running production on Docker Compose who need deployment visibility
- **Edge deployments** where Kubernetes is overkill but manual deploys are unacceptable
- **Anyone** who's tired of SSH-ing into servers to run `docker compose up`

## Development

```bash
# Run the controller locally
go run ./cmd/conops

# Run tests
go test ./...
```

The controller starts on `:8080` with SQLite by default.

## Contributing

Contributions are welcome. Open an issue to discuss larger changes before submitting a PR.

## License

Apache 2.0 &mdash; see [LICENSE](LICENSE).
