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

# 3. final — glibc (debian-slim) + yt-dlp + deno + ffmpeg, non-root.
#
# Songs play a YouTube video referenced inside their soundslice score, and YouTube
# now requires solving a JS "nsig" challenge to fetch the media (otherwise the data
# request 403s). yt-dlp solves it with a JS runtime, and the only reliable provider
# is deno (node reports "unavailable") — which ships NO musl build. The old alpine
# image also pinned yt-dlp to apk's stale 2025.x, which 403s on YouTube regardless
# (it lacks the challenge framework entirely). A glibc base lets us run deno AND a
# current upstream yt-dlp; Vimeo lessons never needed any of this, which is why only
# songs failed. (musl alpine could not run either even with gcompat — see git log.)
FROM debian:trixie-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates tzdata gosu wget passwd \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd -g 911 drumdrop \
    && useradd -u 911 -g drumdrop -d /app -s /usr/sbin/nologin drumdrop

# deno: yt-dlp's JS-challenge solver for YouTube (the only working provider). The
# bin image is a scratch image holding just the static glibc binary.
COPY --from=denoland/deno:bin-2.1.4 /deno /usr/local/bin/deno
# ffmpeg/ffprobe: static build (yt-dlp merges the separate video+audio streams to mp4).
COPY --from=mwader/static-ffmpeg:7.1 /ffmpeg /ffprobe /usr/local/bin/

# yt-dlp: the upstream static binary, re-fetched on every release (the VERSION arg
# busts this layer's cache) so YouTube extraction stays current — the production
# failure was a 7-month-stale yt-dlp. The "&& yt-dlp ... && deno ... && ffmpeg ..."
# smoke test fails the build if any runtime is broken.
ARG VERSION=dev
RUN echo "drumdrop ${VERSION}" \
    && wget -qO /usr/local/bin/yt-dlp https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp_linux \
    && chmod +x /usr/local/bin/yt-dlp \
    && yt-dlp --version && deno --version | head -1 && ffmpeg -version | head -1
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
