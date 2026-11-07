# syntax=docker/dockerfile:1
# Copyright 2026 Mehmet ERGULCU and the abarow contributors
# SPDX-License-Identifier: Apache-2.0

# The image recipe of the Beko X30TR ÖKC adapter. It is a separate container: the register's
# other services (sales, payment...) run without CGO in a scratch image; Token's
# library wants glibc and libusb, so the ÖKC adapter runs in its own
# image, with its own USB device, and talks to the other services over gRPC
# (fiscal.v1). From the repository root:
#
#   docker build -f deploy/container/beko.Containerfile -t abarow-fiscal-beko .
#
# Token's SDK (IntegrationHubWrapper.h and libIntegrationHub.so; 64-bit,
# Ubuntu 18.04+ / GLIBC 2.27+) is not in the repository, its license is unclear
# (docs/decisions/beko-x30tr-prep.md). It is put in the
# services/fiscal/beko/sdk/token/ folder before building; so this image is not published
# until the license question with Token is clear.
#
# Running (with the device connected over USB; for device permission a udev
# rule on the host or the user being in the plugdev group is needed):
#
#   docker run --device /dev/bus/usb -p 9000:9000 \
#     -e ABAROW_BEKO_SECTIONS=1000:1,2000:2 abarow-fiscal-beko

FROM --platform=$BUILDPLATFORM golang:1.27 AS service
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd services/fiscal/beko && \
    CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" GOWORK=off \
    go build -trimpath -ldflags="-s -w" -o /out/service ./cmd/beko

# The bridge and Token's library: Token's .so is published for Ubuntu.
FROM ubuntu:22.04 AS bridge
RUN apt-get update && apt-get install -y --no-install-recommends g++ make libusb-1.0-0-dev libssl-dev zlib1g-dev \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /src/services/fiscal/beko
COPY services/fiscal/beko/hub hub
COPY services/fiscal/beko/sdk/token sdk/token
RUN test -f sdk/token/libIntegrationHub.so && test -f sdk/token/IntegrationHubWrapper.h \
    || { echo "services/fiscal/beko/sdk/token içinde Token'ın IntegrationHubWrapper.h ve libIntegrationHub.so dosyaları yok (bkz. bu dosyanın başı)" >&2; exit 1; }
RUN make -C hub && cp sdk/token/libIntegrationHub.so hub/build/

FROM ubuntu:22.04
RUN apt-get update && apt-get install -y --no-install-recommends libusb-1.0-0 libssl3 zlib1g ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --system --uid 65532 --groups plugdev abarow
COPY --from=service /out/service /service
COPY --from=bridge /src/services/fiscal/beko/hub/build/hubd /usr/local/bin/hubd
COPY --from=bridge /src/services/fiscal/beko/hub/build/libIntegrationHub.so /usr/local/bin/libIntegrationHub.so
USER 65532:65532
ENTRYPOINT ["/service"]
