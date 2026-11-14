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

RUN apk add --no-cache python3 && \
    python3 -m venv /opt/patroni && \
    /opt/patroni/bin/pip install --no-cache-dir "patroni[etcd3]==${PATRONI_VERSION}" "psycopg2-binary==${PSYCOPG2_VERSION}" && \
    ln -s /opt/patroni/bin/patroni /opt/patroni/bin/patronictl /usr/local/bin/

COPY --chmod=0755 deploy/container/patroni/entrypoint.sh /usr/local/bin/abarow-patroni
COPY --chmod=0755 deploy/container/patroni/post-bootstrap.sh /usr/local/bin/abarow-post-bootstrap
# The licenses: abarow's, the notices of the dependencies, and the LGPL-3.0 and GPL-3.0 texts that psycopg2-binary
# (LGPL) asks to be given (THIRD_PARTY_NOTICES, section 3).
COPY --chmod=0644 LICENSE NOTICE THIRD_PARTY_NOTICES deploy/container/patroni/licenses/LGPL-3.0.txt deploy/container/patroni/licenses/GPL-3.0.txt /licenses/

USER postgres
# 5432: PostgreSQL; 8008: Patroni's REST API (HAProxy asks it which machine is primary).
EXPOSE 5432 8008
ENTRYPOINT ["/usr/local/bin/abarow-patroni"]
