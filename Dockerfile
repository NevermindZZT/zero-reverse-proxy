FROM golang:1.23-alpine AS builder

WORKDIR /app

RUN apk add --no-cache ca-certificates git

COPY go.mod ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app/zero-reverse-proxy ./cmd/server

FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

COPY --from=builder /app/zero-reverse-proxy /app/zero-reverse-proxy

ENV PORT=8080 \
    CONFIG_PATH=/data/config.json \
    ADMIN_USER=admin \
    ADMIN_PASS=admin123

EXPOSE 8080
VOLUME ["/data"]

ENTRYPOINT ["/app/zero-reverse-proxy"]
