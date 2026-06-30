# Build Stage
FROM golang:1.26-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git

# Cache Go dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy the source code
COPY . .

# Build the self-contained Go binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o fodmap-detector .

# Run Stage
FROM alpine:latest

WORKDIR /app

# Install runtime dependencies (ca-certificates for HTTPS/TLS, poppler-utils for pdftotext tool)
RUN apk add --no-cache ca-certificates poppler-utils

# Copy the built binary
COPY --from=builder /app/fodmap-detector /app/fodmap-detector

# Copy the service configuration file (service.yaml)
COPY --from=builder /app/service.yaml /app/service.yaml

# Expose the default HTTP service port
EXPOSE 8081

# Set the entrypoint to the binary
ENTRYPOINT ["/app/fodmap-detector"]

# Default command to start the server
CMD ["serve"]
