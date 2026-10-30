# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
ARG TARGETOS TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/spopapi ./cmd/server && \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/spopapi-import ./cmd/importer

FROM alpine:3.22
RUN apk add --no-cache ca-certificates && adduser -D -H -u 10001 spopapi
COPY --from=build /out/spopapi /out/spopapi-import /usr/local/bin/
USER spopapi
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/spopapi"]
