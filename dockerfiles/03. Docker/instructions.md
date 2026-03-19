# Build the alpine stage
`docker build --target alpine -t my-alpine-image .`

# Build the ubuntu stage
`docker build --target ubuntu -t my-ubuntu-image .`

# Build the go-builder stage
`docker build --target go-builder -t my-go-builder-image .`

# Build the final scratch stage
`docker build --target scratch -t my-scratch-image .`


# Running hadolint
```bash
docker run --rm -i hadolint/hadolint < Dockerfile
```

# Running trivy
```bash
trivy image localhost/t
```

# Docker Workshop: Building Small, Secure and Immutable Containers

## Part 1: Understanding Container Fundamentals (20 minutes)

### What Makes a Good Container?
- **Small**: Reduces attack surface, faster to download/deploy
- **Secure**: Minimal vulnerabilities, proper permissions
- **Immutable**: Consistent behavior across environments

### Exploring the Multi-Stage Dockerfile

Review the provided Dockerfile:
```dockerfile
FROM alpine:latest as alpine
RUN echo hello
CMD ["echo", "world"]

FROM ubuntu:latest AS ubuntu
RUN echo hello
CMD ["echo", "world"]

# Use Go compiler from Alpine to build our minimal hello program
FROM golang:alpine AS go-builder
WORKDIR /app
COPY <<EOF ./hello.go
package main

import "fmt"

func main() {
  fmt.Println("hello")
}
EOF

# Build a static binary with no external dependencies
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /hello hello.go

# Copy only the binary to the scratch image
FROM scratch AS scratch
COPY --from=go-builder /hello /hello
CMD ["/hello"]
```

### Exercise 1: Building the Different Stages
```bash
# Build the alpine stage
docker build --target alpine -t my-alpine-image .

# Build the ubuntu stage
docker build --target ubuntu -t my-ubuntu-image .

# Build the go-builder stage
docker build --target go-builder -t my-go-builder-image .

# Build the final scratch stage
docker build --target scratch -t my-scratch-image .
```

## Part 2: Container Size Comparison (20 minutes)

### Exercise 2: Analyzing Image Sizes
```bash
docker images

# Expected output will show dramatic size differences:
# my-scratch-image    Latest    <size>    # Smallest (typically <2MB)
# my-alpine-image     Latest    <size>    # Small (typically ~5MB)
# my-ubuntu-image     Latest    <size>    # Large (typically >70MB)
# my-go-builder-image Latest    <size>    # Largest (typically >300MB)
```

### Discussion Points:
- Why is the scratch image so small?
- What's the trade-off between Alpine and Ubuntu?
- When might you need a larger base image?

### Exercise 3: Running the Containers
```bash
# Run each container and observe the output
docker run --rm my-alpine-image
docker run --rm my-ubuntu-image
docker run --rm my-go-builder-image
docker run --rm my-scratch-image

# Inspect the container filesystems
docker run --rm -it my-alpine-image /bin/sh
# vs
docker run --rm -it my-ubuntu-image /bin/bash
# vs
docker run --rm -it my-scratch-image /bin/sh  # Did this work! Why?
```

## Part 3: Container Security Analysis (25 minutes)

### Introduction to Trivy
Trivy is a vulnerability scanner for containers that detects:
- OS package vulnerabilities
- Language-specific dependencies issues
- Misconfiguration

### Exercise 4: Scanning Images with Trivy
```bash
# Scan the Alpine image
trivy image my-alpine-image

# Scan the Ubuntu image
trivy image my-ubuntu-image

# Scan the Go builder image
trivy image my-go-builder-image

# Scan the scratch image
trivy image my-scratch-image
```

### Discussion Points:
- Compare the vulnerability counts between images
- Understand severity ratings
- Identify why scratch has fewer vulnerabilities
- Strategies for addressing vulnerabilities in base images

## Part 4: Dockerfile Best Practices with Hadolint (20 minutes)

### Introduction to Hadolint
Hadolint analyzes Dockerfiles for best practices and potential issues.

### Exercise 5: Linting Our Dockerfile
```bash
docker run --rm -i hadolint/hadolint < Dockerfile
```

### Common Dockerfile Issues
- Using latest tags instead of specific versions
- Not cleaning up after package installations
- Running as root
- Missing HEALTHCHECK
- Improper layer caching

## Part 5: Container Immutability (20 minutes)

### Principles of Immutable Containers
- Configuration through environment variables
- No runtime changes to container filesystem
- Ephemeral containers
- Data persistence through volumes

## Part 6: Advanced Multi-Stage Builds (20 minutes)

### Benefits of Multi-Stage Builds
- Separate build and runtime environments
- Minimal final image
- Improved security by excluding build tools
- Faster deployments

### Exercise 8: Enhancing Our Multi-Stage Build
Create a more complex example with proper separation of concerns:

```dockerfile
# Builder stage
FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY <<EOF ./main.go
package main

import (
  "fmt"
  "net/http"
  "os"
)

func main() {
  http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
    greeting := os.Getenv("GREETING")
    if greeting == "" {
      greeting = "Hello, World!"
    }
    fmt.Fprintf(w, greeting)
  })

  port := os.Getenv("PORT")
  if port == "" {
    port = "8080"
  }

  fmt.Printf("Server starting on port %s\n", port)
  http.ListenAndServe(":" + port, nil)
}
EOF

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app/server main.go

# Runtime stage
FROM scratch
COPY --from=builder /app/server /server
# Add CA certificates for HTTPS
COPY --from=alpine:latest /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
ENV PORT=8080
ENV GREETING="Hello, Docker Workshop!"
USER 65534:65534
EXPOSE 8080
ENTRYPOINT ["/server"]
```

## Part 7: Security Hardening (20 minutes)

### Common Container Security Issues
- Running as root
- Excessive permissions
- Secrets in images
- Outdated dependencies

### Exercise 9: Implementing Security Best Practices
```dockerfile
FROM alpine:3.18 AS secure-example
# Create non-root user
RUN addgroup -S appgroup && adduser -S appuser -G appgroup

# Set proper permissions
WORKDIR /app
COPY --chown=appuser:appgroup . .

# Remove unnecessary tools
RUN apk --no-cache add curl && \
    # Do something with curl
    apk --no-cache del curl

# Use specific user
USER appuser

# Drop capabilities
# (This would be applied at runtime with docker run --cap-drop=ALL)

CMD ["echo", "Secure container"]
```

## Conclusion and Q&A (15 minutes)

### Key Takeaways
- Start with the smallest base image possible for your needs
- Use multi-stage builds to separate build and runtime environments
- Regularly scan images for vulnerabilities
- Follow linting recommendations for Dockerfiles
- Ensure containers are immutable and properly secured
- Test containers in isolation before deploying

### Best Practices Checklist
- [  ] Use specific image tags, never 'latest'
- [  ] Minimize image layers
- [  ] Clean up package manager caches
- [  ] Run as non-root user
- [  ] Remove unnecessary tools and libraries
- [  ] Scan images regularly
- [  ] Use multi-stage builds
- [  ] Implement proper health checks
- [  ] Keep images immutable
- [  ] Externalize configuration

### Additional Resources
- [Docker Documentation](https://docs.docker.com/)
- [Trivy Documentation](https://aquasecurity.github.io/trivy/)
- [Hadolint Documentation](https://github.com/hadolint/hadolint)
- [OWASP Docker Security Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Docker_Security_Cheat_Sheet.html)
- [Docker Best Practices](https://docs.docker.com/develop/develop-images/dockerfile_best-practices/)
