FROM golang:1.24-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /metrics-agent main.go

# Runtime
FROM alpine:latest
WORKDIR /app
COPY --from=builder /metrics-agent /metrics-agent

CMD ["/metrics-agent"]