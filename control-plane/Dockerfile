# Multi-stage build — final image is ~15MB scratch binary
FROM golang:1.22-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-w -s -X main.Version=$(git describe --tags --always 2>/dev/null || echo dev)" \
    -o /cam-api ./cmd/api

# ─── Final image ─────────────────────────────────────────────────────────────
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata wget

WORKDIR /app
COPY --from=builder /cam-api .

# Non-root user
RUN addgroup -S cam && adduser -S cam -G cam
USER cam

EXPOSE 3001
HEALTHCHECK --interval=15s --timeout=5s --retries=3 \
  CMD wget -qO- http://localhost:3001/health || exit 1

ENTRYPOINT ["/app/cam-api"]
