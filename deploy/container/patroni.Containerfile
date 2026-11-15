# syntax=docker/dockerfile:1
# Copyright 2026 Mehmet ERGULCU and the abarow contributors
# SPDX-License-Identifier: Apache-2.0

# The headquarters database on three machines: PostgreSQL with Patroni (docs/decisions/HA-HEADQUARTERS.md).
# Patroni keeps one machine primary and the others synchronous replicas; when the primary is
# gone, the replica that has every commit takes over. The decision is taken with etcd (majority
# of three machines). The image is the same PostgreSQL as the single-machine headquarters
# (postgres:18-alpine): a data folder moved between them keeps its collation. From the repository root:
#
#   docker build -f deploy/container/patroni.Containerfile -t abarow-patroni .
#
# Settings: deploy/container/patroni/entrypoint.sh.

FROM docker.io/library/postgres:18-alpine

ARG PATRONI_VERSION=4.1.5
ARG PSYCOPG2_VERSION=2.9.13

# psycopg2 is built from its source here, against the libpq of this image (the Alpine package, which Alpine patches),
# instead of taking the ready-made "-binary" wheel, whose bundled OpenSSL and libpq copies Alpine cannot patch.
# The compiler and headers are removed in the same layer; only the runtime libpq (already in the base image) stays.
RUN apk add --no-cache python3 && \
    apk add --no-cache --virtual .psycopg2-build gcc musl-dev python3-dev postgresql-dev && \
    python3 -m venv /opt/patroni && \
    /opt/patroni/bin/pip install --no-cache-dir --no-binary psycopg2 "patroni[etcd3]==${PATRONI_VERSION}" "psycopg2==${PSYCOPG2_VERSION}" && \
    apk del .psycopg2-build && \
    ln -s /opt/patroni/bin/patroni /opt/patroni/bin/patronictl /usr/local/bin/

COPY --chmod=0755 deploy/container/patroni/entrypoint.sh /usr/local/bin/abarow-patroni
COPY --chmod=0755 deploy/container/patroni/post-bootstrap.sh /usr/local/bin/abarow-post-bootstrap
# The licenses: abarow's, the notices of the dependencies, and the LGPL-3.0 and GPL-3.0 texts that psycopg2
# (LGPL) asks to be given (THIRD_PARTY_NOTICES, section 3).
COPY --chmod=0644 LICENSE NOTICE THIRD_PARTY_NOTICES deploy/container/patroni/licenses/LGPL-3.0.txt deploy/container/patroni/licenses/GPL-3.0.txt /licenses/

USER postgres
# 5432: PostgreSQL; 8008: Patroni's REST API (HAProxy asks it which machine is primary).
EXPOSE 5432 8008
ENTRYPOINT ["/usr/local/bin/abarow-patroni"]
