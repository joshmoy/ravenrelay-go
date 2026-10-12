#!/bin/sh
set -eu
export RAVENRELAY_CONTRACT_URL="$1"
cd "$(dirname "$0")/.."
go test -race -count=1 -cover ./...
