# ConOps Architecture

A deep dive into how ConOps manages your applications.

## Overview

ConOps follows the GitOps pattern:
1. **Source of Truth**: Your Git repository defines the desired state.
2. **Reconciliation**: ConOps continuously monitors the cluster (Docker host) and ensures the actual state matches the desired state.
3. **Drift Detection**: Any manual changes (e.g., stopping a container) are detected and corrected automatically.

## Core Components

### 1. Application Registry (Database)

Stores the list of applications to manage. Each application record includes:
- **Repo URL**: Where the source code lives
- **Branch**: Which branch to track
- **Path**: Path to the `docker-compose.yaml` file
- **Credentials**: SSH key or public auth method
- **Sync Status**: Last successful sync, error state, etc.

### 2. Git Watcher (The "Brain")

- **Polling Loop**: Checks the Git remote for new commits at a configurable interval (default: 30s).
- **Clone/Fetch**: Maintains a local checkout of the repository.
- **Change Detection**: Compares the local checkout HEAD with the remote HEAD. If they differ, marks the app as `pending_sync`.

### 3. Reconciler (The "Worker")

Runs continuously and processes apps marked as `pending_sync` or apps that have drifted.

1. **Pull**: Runs `git pull` to update the local checkout.
2. **Validate**: Checks if `docker-compose.yaml` exists and is valid.
3. **Apply**: Executes `docker compose up -d --remove-orphans`.
   - Uses the host Docker daemon via `/var/run/docker.sock`.
   - Applies environment variables if configured.

### 4. Drift Detector (The "Healer")

Runs in parallel with the Reconciler.

- **Monitor**: Queries the Docker API for running containers associated with managed apps.
- **Detect**: Checks for:
  - Missing containers
  - Exited containers (non-zero exit code)
  - Unhealthy containers (failed health checks)
- **Heal**: If drift is detected, triggers a re-reconciliation to bring the app back to the desired state.

## Security Architecture

- **SSH Keys**: Private keys for Git access are encrypted at rest using AES-GCM (if configured).
- **Docker Access**: ConOps requires access to the Docker socket. This is a privileged operation. Ensure ConOps runs in a trusted environment.
- **Network Isolation**: ConOps does not expose managed applications directly. It only manages their lifecycle.

## Data Flow

```mermaid
graph TD
    User[User] -->|Push Commit| Git[Git Repository]
    ConOps[ConOps Controller] -->|Poll (30s)| Git
    ConOps -->|Clone/Fetch| Local[Local Checkout]
    ConOps -->|docker compose up| Docker[Docker Daemon]
    Docker -->|Run| App[Application Containers]
    ConOps -->|Monitor (10s)| App
    App -->|Crash/Drift| ConOps
    ConOps -->|Heal| Docker
```

## Scaling

- **Single Instance**: ConOps is designed as a single-instance controller. Running multiple instances against the same database is not supported and may cause conflicts.
- **Multiple Hosts**: To manage multiple Docker hosts, run one ConOps instance per host.
