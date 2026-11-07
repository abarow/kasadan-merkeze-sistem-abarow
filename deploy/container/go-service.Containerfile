# syntax=docker/dockerfile:1
# Copyright 2026 Mehmet ERGULCU and the abarow contributors
# SPDX-License-Identifier: Apache-2.0

# The common image recipe for Go services (Docker and Podman). From the repository root:
#   docker build -f deploy/container/go-service.Containerfile \
#     --build-arg MODULE=services/core/sales --build-arg PKG=./cmd/sales -t abarow-sales .
# Since Go cross-compiles the ARM image is produced without emulation:
#   docker buildx build --platform linux/arm64 ...

FROM --platform=$BUILDPLATFORM golang:1.27 AS build
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
    go build -trimpath -ldflags="-s -w" -o /out/service "$PKG" && \
    mkdir -p /out/data

FROM scratch
COPY --from=build /out/service /service
COPY --from=build --chown=65532:65532 /out/data /data
USER 65532:65532
ENTRYPOINT ["/service"]
