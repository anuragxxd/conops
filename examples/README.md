# ConOps Examples

This directory contains example configurations for common ConOps use cases.

## Application Configuration Examples

### Basic Public Repository

[`basic-app.json`](./basic-app.json) - Register a public GitHub repository

```bash
curl -X POST http://localhost:8080/api/v1/apps/ \
  -H "Content-Type: application/json" \
  -d @examples/basic-app.json
```

### Private Repository with Deploy Key

[`private-repo.json`](./private-repo.json) - Register a private repository using SSH deploy key

Generate a deploy key:
```bash
ssh-keygen -t ed25519 -C "conops-deploy-key" -f ~/.ssh/conops_deploy_key
# Add the public key to your GitHub repo: Settings > Deploy keys
```

Then use the private key in your app configuration.

## Deployment Examples

### ConOps with PostgreSQL

[`docker-compose-with-postgres.yml`](./docker-compose-with-postgres.yml) - Run ConOps with PostgreSQL for production

```bash
docker compose -f examples/docker-compose-with-postgres.yml up -d
```

This configuration:
- Uses PostgreSQL instead of SQLite
- Properly configures volumes and networking
- Sets up automatic restarts
- Includes health checks

## CLI Examples

### Using conops-ctl

```bash
# List all registered apps
./bin/conops-ctl apps list

# Register an app from JSON file
./bin/conops-ctl apps add examples/basic-app.json

# Delete an app by ID
./bin/conops-ctl apps delete <app-id>
```

## Common Use Cases

### Homelab Setup

1. Run ConOps with SQLite (simplest):
   ```bash
   docker run -d \
     --name conops \
     -p 8080:8080 \
     -v /var/run/docker.sock:/var/run/docker.sock \
     -v conops_data:/data \
     anurag1201/conops:latest
   ```

2. Register your homelab services (Jellyfin, Nextcloud, etc.)
3. Push to Git to auto-deploy updates

### Production Setup

1. Use the PostgreSQL compose file
2. Configure proper backup for the database
3. Set up monitoring and alerting
4. Use deploy keys for private repositories

### Edge Deployment

1. Run ConOps on Raspberry Pi or edge device
2. Point to repos containing edge application configs
3. Benefit from automatic updates and self-healing
