# syntax=docker/dockerfile:1

ARG GO_VERSION=1.25
ARG ALPINE_VERSION=3.21

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build

ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ENV CGO_ENABLED=0
RUN GOARCH=${TARGETARCH} go build -trimpath \
    -ldflags "-s -w \
    -X github.com/BlackRaincoat/versentry/internal/version.Version=${VERSION} \
    -X github.com/BlackRaincoat/versentry/internal/version.Commit=${COMMIT}" \
    -o /out/versentry ./cmd/versentry

FROM alpine:${ALPINE_VERSION}

LABEL org.opencontainers.image.title="Versentry" \
    org.opencontainers.image.description="Notify-only Docker image update monitor. Alerts via Telegram, Discord, webhook, or stdout." \
    org.opencontainers.image.source="https://github.com/BlackRaincoat/versentry" \
    org.opencontainers.image.licenses="MIT"

RUN apk add --no-cache ca-certificates tzdata

# Placeholder so bind-mounting host /etc/hostname → /etc/versentry/hostname
# creates a file mount, not a directory (Docker footgun when target is missing).
RUN mkdir -p /etc/versentry /data && : > /etc/versentry/hostname

# Writable state + health stamp on the /data volume (see docker-compose.example.yml).
ENV VERSENTRY_STATE_FILE=/data/state.json

COPY --from=build /out/versentry /usr/local/bin/versentry

# HEALTHCHECK runs the probe directly (ENTRYPOINT is not applied to it).
RUN printf '%s\n' '#!/bin/sh' 'exec /usr/local/bin/versentry health "$@"' > /usr/local/bin/health \
    && chmod +x /usr/local/bin/health

HEALTHCHECK --interval=30s --timeout=10s --start-period=60s --retries=3 \
    CMD ["/usr/local/bin/health", "-c", "/etc/versentry/config.yaml"]

ENTRYPOINT ["versentry"]
CMD ["run", "-c", "/etc/versentry/config.yaml"]
