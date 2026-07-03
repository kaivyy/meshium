FROM node:20-slim AS frontend
WORKDIR /src/web
COPY web/package*.json ./
RUN npm install
COPY web/ ./
RUN mkdir -p ../cmd/server/web && npm run build

FROM golang:1.23-bookworm AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY internal ./internal
COPY cmd ./cmd
COPY --from=frontend /src/cmd/server/web/build ./cmd/server/web/build
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/meshium ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates && addgroup -S meshium && adduser -S -G meshium -h /data meshium && mkdir -p /data && chown -R meshium:meshium /data
WORKDIR /app
COPY --from=builder /out/meshium /usr/local/bin/meshium
ENV MESHium_PORT=8080 \
    MESHium_DATA_DIR=/data \
    MESHium_LOG_LEVEL=info
EXPOSE 8080
VOLUME ["/data"]
USER meshium
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 CMD wget -qO- http://127.0.0.1:8080/api/health >/dev/null || exit 1
ENTRYPOINT ["/usr/local/bin/meshium"]
