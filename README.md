# Web Terminal

A secure web-based terminal that runs commands in isolated Docker containers.

## Quick Reference Commands

### Docker Commands

Build the base image for a specific platform:
```bash
docker build --platform "linux/arm64" -t terminal-base:latest --file Dockerfile.base .
```

Remove all containers based on Alpine image:
```bash
docker rm -f $(docker ps -a --filter "ancestor=alpine" -q)
```

## Development

More details about development and setup will be added here.

## License

[Add your license information here]