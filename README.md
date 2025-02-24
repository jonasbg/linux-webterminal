# Web Terminal

A secure web-based terminal that runs commands in isolated Docker containers.

## Quick Reference Commands

### Docker Commands

Build the base image for a specific platform:

```bash
docker build --platform "linux/amd64" -t terminal-base:latest --file Dockerfile.base .
```

Remove all containers based on Alpine image:

```bash
docker rm -f $(docker ps -a --filter "ancestor=terminal-base" -q)
```

Build the container for running the server

```bash
docker build -t terminal-server .
```

Run the server in docker

```bash
docker run -p 5001:5000 --rm -it -v /var/run/docker.sock:/var/run/docker.sock --name terminal-server terminal-server
```

List all uniq IP addresses from your logs:

```bash
grep -hoP 'Origin IP: \K[\d\.]+' logs/*.log | sort -u
```

### Environments

Here's a Markdown table containing the extracted environment variables, their default values, and descriptions:

| Environment Variable     | Default Value | Description |
|--------------------------|--------------|-------------|
| `HOST`                   | `*`          | Defines the CORS allowed origins. |
| `TTY_LOGGING_ENABLED`    | `false`      | Enables or disables logging for terminal sessions. |
| `TTY_LOG_DIR`            | `./logs`     | Directory where session logs are stored. |
| `MAX_CONTAINERS`         | `10`         | Maximum number of terminal containers allowed. |
| `CONTAINER_LIFETIME`     | `3600`       | Lifetime (in seconds) before a container is automatically cleaned up. |
| `CONTAINER_IMAGE`        | `ghcr.io/jonasbg/linux-webterminal/terminal-base:latest` | Docker image used for terminal sessions. |

## Development

More details about development and setup will be added here.

```bash
ls Dockerfile.base | entr -s 'docker build -t terminal-base . --file Dockerfile.base'
```

## License

[Add your license information here]
