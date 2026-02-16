# Installation Guide

ConOps can be installed via Docker (recommended), as a binary, or built from source.

## Table of Contents
- [Docker (Recommended)](#docker-recommended)
- [Docker Compose](#docker-compose)
- [Binary Installation](#binary-installation)
- [Building from Source](#building-from-source)
- [Configuration Reference](#configuration-reference)

## Docker (Recommended)

The easiest way to run ConOps is using the official Docker image.

```bash
docker run -d \
  --name conops \
  -p 8080:8080 \
  -e CONOPS_RUNTIME_DIR=/tmp/conops-runtime \
  -v /tmp/conops-runtime:/tmp/conops-runtime \
  -v conops_data:/data \
  -v /var/run/docker.sock:/var/run/docker.sock \
  anurag1201/conops:latest
```

### Explanation of flags:
- `-p 8080:8080`: Exposes the web UI and API on port 8080
- `-e CONOPS_RUNTIME_DIR=/tmp/conops-runtime`: Sets the directory where ConOps clones repos
- `-v /tmp/conops-runtime:/tmp/conops-runtime`: **CRITICAL** - Maps the runtime directory so the host docker daemon can access the compose files
- `-v conops_data:/data`: Persists the SQLite database and encryption keys
- `-v /var/run/docker.sock:/var/run/docker.sock`: Allows ConOps to control the host Docker daemon

## Docker Compose

For a more permanent setup, use Docker Compose.

```yaml
version: '3.8'

services:
  conops:
    image: anurag1201/conops:latest
    container_name: conops
    ports:
      - "8080:8080"
    environment:
      - CONOPS_RUNTIME_DIR=/tmp/conops-runtime
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - /tmp/conops-runtime:/tmp/conops-runtime
      - conops_data:/data
    restart: unless-stopped

volumes:
  conops_data:
```

Run with:
```bash
docker compose up -d
```

## Binary Installation

You can run the single binary directly on your host if you prefer.

1. Download the latest release from [GitHub Releases](https://github.com/anuragxxd/conops/releases)
2. Make it executable:
   ```bash
   chmod +x conops
   ```
3. Run it:
   ```bash
   ./conops
   ```

Note: When running as a binary, ensure `docker` and `docker compose` (plugin) are installed and available in your PATH.

## Building from Source

Prerequisites:
- Go 1.21+
- Docker
- Git

```bash
git clone https://github.com/anuragxxd/conops.git
cd conops
go build -o conops ./cmd/conops
./conops
```

## Configuration Reference

ConOps is configured via environment variables.

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8080` | Port to listen on |
| `DB_TYPE` | `sqlite` | Storage backend: `sqlite` or `postgres` |
| `DB_CONNECTION_STRING` | &mdash; | Connection string for Postgres |
| `CONOPS_RUNTIME_DIR` | `./.conops-runtime` | Directory for checking out repos |
| `CONOPS_RECONCILE_INTERVAL` | `10s` | How often to check for drift |
| `CONOPS_SYNC_TIMEOUT` | `5m` | Timeout for git/docker operations |
| `CONOPS_LOG_LEVEL` | `info` | Logging level (`debug`, `info`, `warn`, `error`) |

### Production Configuration (PostgreSQL)

For production environments, we recommend using PostgreSQL instead of SQLite.

```bash
docker run -d \
  --name conops \
  -p 8080:8080 \
  -e DB_TYPE=postgres \
  -e DB_CONNECTION_STRING="postgres://user:pass@host:5432/conops?sslmode=disable" \
  ...
```
