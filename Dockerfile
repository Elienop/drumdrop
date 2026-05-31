# 1. web — build the SPA
FROM node:20-alpine AS web-builder
WORKDIR /app/web
COPY web/package*.json ./
RUN npm ci
COPY web/ .
RUN npm run build

# 2. go — embed dist, static binary (no cgo)
FROM golang:1.26-alpine AS go-builder
ARG VERSION=dev
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web-builder /app/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -tags webui -ldflags "-s -w -X main.version=${VERSION}" \
    -o drumdrop ./cmd/drumdrop

# 3. final — alpine + yt-dlp + ffmpeg, non-root
FROM alpine:3.22
# yt-dlp and ffmpeg both come from alpine's community repo (enabled by default).
# We install yt-dlp from apk rather than the upstream "static" yt-dlp_linux binary
# because that upstream binary is glibc-linked (interpreter /lib64/ld-linux-x86-64.so.2,
# bundled libpython needs glibc fortify/posix_fallocate64 symbols) and therefore does
# NOT run on a musl alpine base — even with gcompat. The apk build is musl-native.
RUN apk add --no-cache ca-certificates tzdata su-exec shadow ffmpeg yt-dlp \
    && addgroup -g 911 drumdrop && adduser -u 911 -G drumdrop -D drumdrop \
    && yt-dlp --version
WORKDIR /app
COPY --from=go-builder /app/drumdrop .
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh && mkdir -p /config /downloads && chown -R drumdrop:drumdrop /config /downloads /app
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
    CMD wget -q --spider http://127.0.0.1:8080/readyz || exit 1
ENV DRUMDROP_CONFIG_DIR=/config \
    DRUMDROP_DOWNLOADS_DIR=/downloads \
    DRUMDROP_LISTEN=0.0.0.0:8080
ENTRYPOINT ["/entrypoint.sh"]
CMD ["./drumdrop", "serve"]
