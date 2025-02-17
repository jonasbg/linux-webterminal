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

## Development

More details about development and setup will be added here.

## License

[Add your license information here]