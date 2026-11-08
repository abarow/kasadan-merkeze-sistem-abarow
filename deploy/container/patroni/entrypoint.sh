#!/bin/sh
# Copyright 2026 Mehmet ERGULCU and the abarow contributors
# SPDX-License-Identifier: Apache-2.0

# Writes Patroni's configuration and starts Patroni (docs/decisions/HA-HEADQUARTERS.md).
#
#   ABAROW_NODE          this machine's name in the cluster (e.g. hq-1)
#   ABAROW_NODE_ADDRESS  the name or IP the other machines reach this one at
#   ABAROW_ETCD          the etcd addresses of the three machines (host:2379,...)
#   /run/secrets/db-password           the password of the abarow user (the services)
#   /run/secrets/replication-password  the password the replicas connect with
#
# The arguments are the database settings in the form "-c name=value" (reporting
# pgtune, the same as the single-machine headquarters). They are written to the cluster's
# shared settings when the cluster is created: some of them (max_connections,
# max_worker_processes) must be the same on all the machines. Later changes are made with
# "patronictl edit-config", not by restarting with other arguments.
#
# Commits are synchronous and strict: a commit does not complete before at least one replica
# has written it. If both replicas are gone the primary stops accepting writes; the events
# wait at the stores instead of being written to one machine only.
set -eu

: "${ABAROW_NODE:?ABAROW_NODE is not set}"
: "${ABAROW_NODE_ADDRESS:?ABAROW_NODE_ADDRESS is not set}"
: "${ABAROW_ETCD:?ABAROW_ETCD is not set}"
for f in /run/secrets/db-password /run/secrets/replication-password; do
	[ -s "$f" ] || { echo "abarow-patroni: $f is missing or empty" >&2; exit 1; }
done
PATRONI_SUPERUSER_PASSWORD=$(head -n 1 /run/secrets/db-password)
PATRONI_REPLICATION_PASSWORD=$(head -n 1 /run/secrets/replication-password)
export PATRONI_SUPERUSER_PASSWORD PATRONI_REPLICATION_PASSWORD

# YAML single-quoted string: ' is written twice.
quote() { printf "'%s'" "$(printf '%s' "$1" | sed "s/'/''/g")"; }

params=
while [ $# -gt 0 ]; do
	case "$1" in
	-c)
		[ $# -ge 2 ] || { echo "abarow-patroni: -c without a value" >&2; exit 1; }
		case "$2" in
		*=*) ;;
		*) echo "abarow-patroni: invalid setting: $2 (name=value)" >&2; exit 1 ;;
		esac
		params="$params
        ${2%%=*}: $(quote "${2#*=}")"
		shift 2
		;;
	*) echo "abarow-patroni: unknown argument: $1 (-c name=value)" >&2; exit 1 ;;
	esac
done

etcd_hosts=
for h in $(printf '%s' "$ABAROW_ETCD" | tr ',' ' '); do
	etcd_hosts="$etcd_hosts${etcd_hosts:+, }$(quote "$h")"
done

conf=/tmp/patroni.yml
umask 077
cat >"$conf" <<EOF
scope: abarow-headquarters
name: $(quote "$ABAROW_NODE")
restapi:
  listen: 0.0.0.0:8008
  connect_address: $(quote "$ABAROW_NODE_ADDRESS:8008")
etcd3:
  hosts: [$etcd_hosts]
bootstrap:
  dcs:
    ttl: 30
    loop_wait: 10
    retry_timeout: 10
    synchronous_mode: true
    synchronous_mode_strict: true
    postgresql:
      use_pg_rewind: true
      use_slots: true
      parameters:
        wal_level: replica
        hot_standby: 'on'
        wal_log_hints: 'on'
        max_wal_senders: 10
        max_replication_slots: 10$params
  post_bootstrap: /usr/local/bin/abarow-post-bootstrap
postgresql:
  listen: 0.0.0.0:5432
  connect_address: $(quote "$ABAROW_NODE_ADDRESS:5432")
  data_dir: /var/lib/postgresql/data
  pgpass: /tmp/pgpass
  authentication:
    superuser:
      username: abarow
    replication:
      username: replicator
  pg_hba:
    - local all all trust
    - host replication replicator all scram-sha-256
    - host all all all scram-sha-256
EOF
exec patroni "$conf"
