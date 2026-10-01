#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
mkdir -p data/raw
curl -fsSL -o data/raw/airports.csv https://davidmegginson.github.io/ourairports-data/airports.csv
curl -fsSL -o data/raw/routes.dat https://raw.githubusercontent.com/jpatokal/openflights/master/data/routes.dat
curl -fsSL -o data/raw/openflights_airports.dat https://raw.githubusercontent.com/jpatokal/openflights/master/data/airports.dat
go run ./cmd/builddata
