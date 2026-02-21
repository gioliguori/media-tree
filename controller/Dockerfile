FROM golang:1.25-alpine AS builder

WORKDIR /app

# Dipendenze build
RUN apk add --no-cache git

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build
RUN CGO_ENABLED=0 GOOS=linux go build -o /controller ./cmd/controller

# RUNTIME STAGE
FROM alpine:3.19

WORKDIR /app

# Docker CLI
RUN apk add --no-cache docker-cli

# Copy binary
COPY --from=builder /controller /controller

# Copia directory web
COPY --from=builder /app/web ./web

# Expose API port
EXPOSE 8080

# Run
CMD ["/controller"]