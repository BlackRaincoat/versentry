# syntax=docker/dockerfile:1

ARG GO_VERSION=1.25

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build

ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown

WORKDIR /src

# CA bundle for the scratch runtime (TLS to registries and notifiers).
RUN apk add --no-cache ca-certificates \
    && mkdir -p /out/etc/versentry /out/data \
    && : > /out/etc/versentry/hostname

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ENV CGO_ENABLED=0
RUN GOARCH=${TARGETARCH} go build -trimpath \
    -ldflags "-s -w \
    -X github.com/BlackRaincoat/versentry/internal/version.Version=${VERSION} \
    -X github.com/BlackRaincoat/versentry/internal/version.Commit=${COMMIT}" \
    -o /out/versentry ./cmd/versentry

FROM scratch

LABEL org.opencontainers.image.title="Versentry" \
    org.opencontainers.image.description="Notify-only Docker image update monitor. Alerts via Telegram, Discord, webhook, or stdout." \
    org.opencontainers.image.source="https://github.com/BlackRaincoat/versentry" \
    org.opencontainers.image.licenses="MIT"

# Writable state + health stamp on the /data volume (see docker-compose.example.yml).
ENV VERSENTRY_STATE_FILE=/data/state.json

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/etc/versentry/hostname /etc/versentry/hostname
COPY --from=build /out/data /data
COPY --from=build /out/versentry /usr/local/bin/versentry

# HEALTHCHECK runs the probe directly (ENTRYPOINT is not applied to it).
HEALTHCHECK --interval=30s --timeout=10s --start-period=60s --retries=3 \
    CMD ["/usr/local/bin/versentry", "health", "-c", "/etc/versentry/config.yaml"]

ENTRYPOINT ["/usr/local/bin/versentry"]
CMD ["run", "-c", "/etc/versentry/config.yaml"]
