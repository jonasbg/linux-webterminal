# Build stage
FROM golang:1.23 AS builder

# Set working directory
WORKDIR /app

# Copy go.mod and go.sum
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application with optimizations
# CGO_ENABLED=0 creates a statically linked binary that can run in scratch
RUN go env -w GO111MODULE=on && \
    CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -ldflags="-s -w" -o webterminal .

# Runtime stage - using scratch (completely empty base image)
FROM scratch

# Copy SSL certificates for Docker API HTTPS connections
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Copy the compiled binary
COPY --from=builder /app/webterminal /webterminal

# Copy web assets
COPY --from=builder /app/templates /web/templates
COPY --from=builder /app/static /web/static

USER 1000:1000

# Expose the application port
EXPOSE 8080
ENV GIN_MODE=release

# Command to run the application
ENTRYPOINT ["/webterminal"]