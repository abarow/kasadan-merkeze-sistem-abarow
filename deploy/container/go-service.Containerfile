# syntax=docker/dockerfile:1
# Copyright 2026 Mehmet ERGULCU and the abarow contributors
# SPDX-License-Identifier: Apache-2.0

# The common image recipe for Go services (Docker and Podman). From the repository root:
#   docker build -f deploy/container/go-service.Containerfile \
#     --build-arg MODULE=services/core/sales --build-arg PKG=./cmd/sales -t abarow-sales .
# Since Go cross-compiles the ARM image is produced without emulation:
#   docker buildx build --platform linux/arm64 ...

# The Go version is the toolchain line of go.mod; govulncheck in CI fails when a newer
# patch fixes a vulnerability, then both are raised together.
FROM --platform=$BUILDPLATFORM golang:1.27.4@sha256:08bcb99a38f85a265f9a93d1d3323e2440eec333380f3ea26ee0dec9264bb41c AS build
ARG VERSION=dev
ARG MODULE
ARG PKG
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY . .
# The modules are tied to the other modules in the workspace with replace; go.work
# gerek olmadan derlenir.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd "$MODULE" && \
    CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" GOWORK=off \
    go build -trimpath -ldflags="-s -w -X abarow.com/pkg/service.Version=$VERSION" -o /out/service "$PKG" && \
    mkdir -p /out/data

FROM scratch
COPY --from=build /out/service /service
COPY --from=build --chown=65532:65532 /out/data /data
USER 65532:65532
ENTRYPOINT ["/service"]
