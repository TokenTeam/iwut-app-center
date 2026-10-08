# syntax=docker/dockerfile:1.7

FROM golang:1.24-alpine AS build

ARG TARGETOS=linux
ARG TARGETARCH

WORKDIR /src

# Copy module metadata first so dependency downloads remain cached when source
# files change. The root module replaces the API dependency with ./api.
COPY go.mod go.sum ./
COPY api/go.mod api/go.sum ./api/
RUN go mod download

COPY api ./api
COPY cmd ./cmd
COPY internal ./internal

# App Center currently has no CGO dependency. Disabling CGO produces a static
# binary with no runtime dependency on either glibc or musl.
RUN if [ -n "$TARGETARCH" ]; then export GOARCH="$TARGETARCH"; fi; \
    CGO_ENABLED=0 GOOS="$TARGETOS" \
    go build -trimpath -ldflags="-s -w -buildid=" \
    -o /out/app-center ./cmd/app-center

FROM alpine:3.22 AS runtime

RUN apk add --no-cache ca-certificates \
    && addgroup -S -g 10001 app-center \
    && adduser -S -D -H -u 10001 -G app-center app-center

WORKDIR /app
COPY --from=build --chown=app-center:app-center /out/app-center ./app-center

USER app-center:app-center

EXPOSE 8080 9090
STOPSIGNAL SIGTERM

ENTRYPOINT ["/app/app-center"]
CMD ["serve"]
