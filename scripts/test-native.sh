#!/usr/bin/env bash
set -Eeuo pipefail
ROOT=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd "$ROOT"
mkdir -p bin/tools
if [[ -z ${XRAY_BINARY:-} ]]; then
    XRAY_VERSION=$(go list -m -f '{{.Version}}' github.com/xtls/xray-core)
    GOBIN="$ROOT/bin/tools" go install "github.com/xtls/xray-core/main@$XRAY_VERSION"
    XRAY_BINARY=$ROOT/bin/tools/main
fi
export XRAY_BINARY
export PROXYK_BINARY=$ROOT/bin/proxyk-admin
go build -trimpath -o "$PROXYK_BINARY" ./cmd/server
go test -race -count=1 -v ./tests/integration
