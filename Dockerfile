# Multi-stage build for Galactic Bloodshed

# Stage 1: Build all Go binaries
FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY . .

RUN cd go && \
    go build -o /build/makeuniv ./cmd/makeuniv && \
    go build -o /build/gb-server ./cmd/gb-server && \
    go build -o /build/racegen ./cmd/racegen

# Stage 2: Runtime image
FROM alpine:latest

# Install runtime dependencies
RUN apk add --no-cache ca-certificates

# Copy binaries from builder
COPY --from=builder /build/makeuniv /usr/local/bin/makeuniv
COPY --from=builder /build/gb-server /usr/local/bin/gb-server
COPY --from=builder /build/racegen /usr/local/bin/racegen

# Create data directory
RUN mkdir -p /data

# Default command is gb-server
ENTRYPOINT []
CMD ["gb-server"]
