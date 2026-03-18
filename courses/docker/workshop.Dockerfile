FROM alpine:latest as alpine

RUN echo hello

CMD ["echo", "hello world!"]

FROM ubuntu:latest AS ubuntu

RUN echo hello

CMD ["echo", "hello world!"]

# Use Go compiler from Alpine to build our minimal hello program
FROM golang:alpine AS go-builder
WORKDIR /app
COPY <<EOF ./hello.go
package main

import "fmt"

func main() {
  fmt.Println("hello world!")
}
EOF

# Build a static binary with no external dependencies
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /hello hello.go

# Copy only the binary to the scratch image
FROM scratch AS scratch
COPY --from=go-builder /hello /hello
CMD ["/hello"]

