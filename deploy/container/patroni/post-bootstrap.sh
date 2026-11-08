#!/bin/sh
# Copyright 2026 Mehmet ERGULCU and the abarow contributors
# SPDX-License-Identifier: Apache-2.0

# Patroni runs this once, on the machine that creates the cluster, with the connection
# address of the new database: the services' database is created (on the single-machine
# headquarters the postgres image does this with POSTGRES_DB).
set -eu
psql "$1" -v ON_ERROR_STOP=1 -c "CREATE DATABASE abarow"
