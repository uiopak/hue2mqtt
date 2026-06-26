# Stage 1: Build the Go binary
FROM golang:1.26-alpine AS builder

WORKDIR /app

# Copy go.mod and go.sum first to cache dependency downloads
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source code
COPY . .

# Build a statically linked Go binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o hue2mqtt main.go

# Stage 2: Minimal scratch deployment image
FROM scratch

# Copy statically compiled binary
COPY --from=builder /app/hue2mqtt /hue2mqtt

# Copy SSL certificates (needed for secure outgoing requests like HTTPS)
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Expose HTTP port 80 and SSDP discovery port 1900/udp
EXPOSE 80
EXPOSE 1900/udp

# Run the emulator
ENTRYPOINT ["/hue2mqtt"]
