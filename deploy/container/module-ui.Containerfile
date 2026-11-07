# syntax=docker/dockerfile:1
# Copyright 2026 Mehmet ERGULCU and the abarow contributors
# SPDX-License-Identifier: Apache-2.0

# The image recipe for vertical modules that have a screen: the screen (ui/apps/<module>) is built with Node,
# the service with Go; the screen files are put in /web. From the repository root:
#   docker build -f deploy/container/module-ui.Containerfile \
#     --build-arg MODULE=services/modules/retail --build-arg PKG=./cmd/retail \
#     --build-arg UI=ui/apps/retail -t abarow-retail .

# Node 24 LTS (destek sonu 30.04.2028).
# The shared parts of the register screens (ui/packages) stay in their place in the repository:
# the screen finds them by the ../../packages path.
FROM --platform=$BUILDPLATFORM node:24-alpine AS ui
ARG UI
WORKDIR /w/${UI}
COPY ${UI}/package.json ${UI}/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci --no-audit --no-fund
COPY ui/packages /w/ui/packages
COPY ${UI}/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.27 AS build
ARG MODULE
ARG PKG
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd "$MODULE" && \
    CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" GOWORK=off \
    go build -trimpath -ldflags="-s -w" -o /out/service "$PKG" && \
    mkdir -p /out/data

FROM scratch
ARG UI
COPY --from=build /out/service /service
COPY --from=build --chown=65532:65532 /out/data /data
COPY --from=ui /w/${UI}/dist /web
USER 65532:65532
ENTRYPOINT ["/service"]
