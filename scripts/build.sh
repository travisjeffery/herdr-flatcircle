#!/bin/sh
# Herdr runs this as the plugin's build step.
set -eu
cd "$(dirname "$0")/.."
command -v go >/dev/null 2>&1 || { echo "shepherd build: go is not installed" >&2; exit 1; }
go build -o bin/shepherd .
