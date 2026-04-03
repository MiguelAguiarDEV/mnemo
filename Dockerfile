FROM golang:1.25-alpine AS builder
RUN apk add --no-cache git
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /engram ./cmd/engram

FROM alpine:3.21
RUN apk add --no-cache ca-certificates bash coreutils findutils grep git
WORKDIR /app
COPY --from=builder /engram /usr/local/bin/engram
HEALTHCHECK --interval=30s --timeout=5s --retries=3 \
  CMD wget -qO- http://localhost:8080/health || exit 1
CMD ["engram", "cloud", "serve", "--host", "0.0.0.0", "--port", "8080"]
