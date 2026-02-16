# Troubleshooting Guide

Common issues and solutions when running ConOps.

## Table of Contents
- [Docker Connectivity Issues](#docker-connectivity-issues)
- [Git Repository Issues](#git-repository-issues)
- [Database Issues](#database-issues)
- [Debugging](#debugging)

## Docker Connectivity Issues

### "Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?"

**Cause:** ConOps cannot access the Docker socket.

**Fix:** Ensure you mount the socket:
```bash
-v /var/run/docker.sock:/var/run/docker.sock
```

### "Permission denied while trying to connect to the Docker daemon socket"

**Cause:** The user running ConOps inside the container does not have permission to access the socket.

**Fix:** Run as root (default) or ensure the user is in the `docker` group. If running as a binary, run with `sudo` or add your user to the `docker` group.

## Git Repository Issues

### "Authentication failed" for Private Repos

**Cause:** SSH key not added or incorrect permissions.

**Fix:**
1. Ensure the SSH key is added as a **Deploy Key** in your GitHub repository settings.
2. Ensure the key format is correct (ED25519 recommended).
3. Check the logs for `ssh: handshake failed`.

### "Repository not found"

**Cause:** URL is incorrect or access is denied.

**Fix:**
- Verify the URL works: `git ls-remote <url>`
- Ensure the deploy key has read access.

## Database Issues

### "database is locked" (SQLite)

**Cause:** Multiple processes trying to write to the SQLite file simultaneously.

**Fix:**
- Ensure only one instance of ConOps is running against the database file.
- Switch to PostgreSQL for high-concurrency environments.

### "pq: password authentication failed for user" (PostgreSQL)

**Cause:** Incorrect credentials in `DB_CONNECTION_STRING`.

**Fix:** Verify your connection string format:
`postgres://user:password@host:5432/dbname?sslmode=disable`

## Debugging

### Enable Debug Logs

Set `CONOPS_LOG_LEVEL=debug` to see verbose logs, including git operations and docker commands.

```bash
docker run -e CONOPS_LOG_LEVEL=debug ...
```

### Check Internal State

You can inspect the internal state by querying the API directly:

```bash
curl http://localhost:8080/api/v1/apps
```

### Common Log Messages

- `reconciling app`: Normal operation, checking for drift.
- `syncing app`: Pulling latest changes from git.
- `drift detected`: Container state does not match desired state.
- `executing compose up`: Applying changes.
