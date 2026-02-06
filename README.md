# ConOps

ConOps brings GitOps-style workflows to Docker Compose.
It gives you an Argo CD-like control plane for teams that are not running
Kubernetes, but still want repo-driven deployments, continuous reconciliation,
and a clean operational UI.

![ConOps logo](./web/static/assets/conops.png)

## Why ConOps

If your stack runs on Docker Compose, you still need safe, repeatable, and
observable deploys. ConOps gives you that without introducing Kubernetes.

- Track app definitions in Git.
- Detect new commits automatically.
- Reconcile runtime state continuously.
- Recover from container drift.
- Deploy public repos and private GitHub repos with deploy keys.

## What you get

ConOps is a single controller process with a web UI and API.
It focuses on practical GitOps for lightweight self-hosted environments.

- **UI dashboard** at `/ui/apps` to register, inspect, sync, and delete apps.
- **REST API** at `/api/v1/apps` for automation.
- **Git watcher** that polls configured branches and marks apps pending on new
  commits.
- **Reconciler loop** that runs `docker compose pull` and
  `docker compose up -d --remove-orphans`.
- **Runtime drift checks** that requeue apps when containers are missing,
  unhealthy, or exited.
- **Encrypted deploy key storage** for private repository access.
- **SQLite or PostgreSQL storage** depending on your deployment mode.

## Quick start with Docker Compose

The fastest way to run ConOps is with the included `docker-compose.yml`.
This starts the controller and PostgreSQL.

1. Start ConOps.

   ```bash
   docker compose up --build
   ```

2. Open the dashboard.

   ```text
   http://localhost:8080/ui/apps
   ```

3. Register your first app from **New App** in the UI.

4. Push a commit to your tracked branch and watch ConOps reconcile it.

## Quick start with local binaries

If you prefer local binaries, run ConOps directly on your host.
In this mode, SQLite is used by default.

1. Build controller and CLI binaries.

   ```bash
   go build -o ./bin/conops ./cmd/conops
   go build -o ./bin/conops-ctl ./cmd/conops-ctl
   ```

2. Start the controller.

   ```bash
   ./bin/conops
   ```

3. In a new terminal, list apps through the CLI.

   ```bash
   ./bin/conops-ctl apps list
   ```

## Register an app via API

You can automate registration with a JSON payload.
ConOps defaults `branch` to `main`, `compose_path` to `compose.yaml`, and
`poll_interval` to `30s` when omitted.

```bash
cat > /tmp/app.json <<'JSON'
{
  "name": "whoami",
  "repo_url": "https://github.com/traefik/whoami.git",
  "repo_auth_method": "public",
  "branch": "main",
  "compose_path": "compose.yaml",
  "poll_interval": "30s"
}
JSON

curl -X POST http://localhost:8080/api/v1/apps/ \
  -H "Content-Type: application/json" \
  --data @/tmp/app.json
```

List apps:

```bash
curl http://localhost:8080/api/v1/apps/
```

Force sync by app ID:

```bash
curl -X POST http://localhost:8080/api/v1/apps/<app-id>/sync
```

Delete an app by app ID:

```bash
curl -X DELETE http://localhost:8080/api/v1/apps/<app-id>
```

## Private repo support with deploy keys

ConOps supports private repositories using GitHub deploy keys.
For this mode, use an SSH repo URL and provide the private key on registration.

```json
{
  "name": "private-app",
  "repo_url": "git@github.com:your-org/private-repo.git",
  "repo_auth_method": "deploy_key",
  "deploy_key": "-----BEGIN OPENSSH PRIVATE KEY-----\n...\n-----END OPENSSH PRIVATE KEY-----",
  "branch": "main",
  "compose_path": "deploy/compose.yaml"
}
```

Deploy key requirements:

- Host must be `github.com`.
- URL must be SSH format.
- Passphrase-protected private keys are not supported.
- Keys are encrypted at rest.

## Configuration

ConOps behavior is controlled through environment variables.
These values are read by the controller process.

| Variable | Default | Description |
| --- | --- | --- |
| `DB_TYPE` | `sqlite` | `sqlite` or `postgres` |
| `DB_CONNECTION_STRING` | empty | Required when `DB_TYPE=postgres` |
| `CONOPS_RECONCILE_INTERVAL` | `10s` | Interval for reconcile loop |
| `CONOPS_SYNC_TIMEOUT` | `5m` | Max duration of one sync |
| `CONOPS_RETRY_ERRORS` | `false` | Retry apps in `error` status |
| `CONOPS_ENCRYPTION_KEY` | empty | Optional 32-byte raw or base64 key |
| `CONOPS_ENCRYPTION_KEY_FILE` | `/data/conops-encryption.key` (container) | Path for generated/read encryption key |
| `CONOPS_KNOWN_HOSTS_FILE` | auto-resolved | Known hosts file for strict SSH verification |

## How reconciliation works

ConOps separates change detection from runtime apply.
This keeps the control loop predictable and easy to reason about.

```mermaid
flowchart LR
  A["Git watcher polls repo branch"] --> B["New commit detected"]
  B --> C["App marked pending"]
  C --> D["Reconciler marks syncing"]
  D --> E["Clone/fetch repo and resolve compose path"]
  E --> F["docker compose pull + up -d"]
  F --> G["App marked synced"]
  G --> H["Drift check keeps runtime healthy"]
```

Typical status flow:

- `registered` when app is created.
- `pending` when a new commit is detected or drift is found.
- `syncing` while apply runs.
- `synced` when apply succeeds.
- `error` when apply fails.

## API and UI endpoints

ConOps exposes both machine and human interfaces.
Use the API for automation and the UI for fast operations.

- `GET /` redirects to `/ui/apps`.
- `GET /ui/apps` shows the app dashboard.
- `GET /ui/apps/new` opens app registration.
- `GET /api/v1/apps/` lists apps.
- `POST /api/v1/apps/` creates an app.
- `GET /api/v1/apps/{id}` gets app details.
- `POST /api/v1/apps/{id}/sync` force syncs an app.
- `DELETE /api/v1/apps/{id}` deletes an app and runtime resources.

## Media you should add before public launch

Strong media improves trust and adoption.
For launch posts and README previews, add one short video and two screenshots.

- `docs/media/conops-dashboard.png`: app list page.
- `docs/media/conops-app-detail.png`: app detail with status and commit.
- `docs/media/conops-register-app.png`: app registration form.
- `docs/media/conops-demo.gif`: 20 to 45 second end-to-end flow.

When files are ready, embed them near the top of this README:

```md
![ConOps dashboard](docs/media/conops-dashboard.png)
![ConOps app detail](docs/media/conops-app-detail.png)
```

## Positioning for users

ConOps is best for teams that want GitOps outcomes without Kubernetes.
It is effective for edge, homelab, SMB infrastructure, and internal tools.

- "Argo CD for Docker Compose."
- "Git is your control plane, Compose is your runtime."
- "Continuous reconciliation for non-Kubernetes stacks."

## Development

You can run the controller and API locally during development.
The service listens on port `8080`.

```bash
go run ./cmd/conops
```

To run tests:

```bash
GOCACHE=$(pwd)/.conops-cache/go-build go test ./...
```

## License

This repository does not currently include a `LICENSE` file.
Add one before public distribution so users know reuse terms.

