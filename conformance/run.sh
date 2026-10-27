#!/usr/bin/env bash
set -euo pipefail
# Run from the Cordis module root. Builds real independently authored plugins.
work="$(mktemp -d "${TMPDIR:-/tmp}/cordis-conformance.XXXXXX")"
trap 'rm -rf "$work"' EXIT
printf 'Platform: '
go env GOOS GOARCH GOVERSION
go test -tags=integration -race ./... -timeout 5m
go build -o "$work/worker" ./cmd/cordis-worker
go build -o "$work/basic-host" ./examples/basic-host
go build -o "$work/lifecycle-smoke" ./conformance/cmd/lifecycle-smoke
(
  cd examples/plugins/counter
  go build -o "$work/counter" .
  GOOS=wasip1 GOARCH=wasm go build -o "$work/counter.wasm" .
)
(
  cd examples/plugins/chain
  go build -o "$work/chain" .
  GOOS=wasip1 GOARCH=wasm go build -o "$work/chain.wasm" .
  go build -ldflags '-X main.rejectInitialization=true' -o "$work/chain-bad" .
  GOOS=wasip1 GOARCH=wasm go build -ldflags '-X main.rejectInitialization=true' -o "$work/chain-bad.wasm" .
)
"$work/basic-host" -worker "$work/worker" -native "$work/counter" -wasm "$work/counter.wasm"
"$work/lifecycle-smoke" -runtime native -guest "$work/chain" -bad-guest "$work/chain-bad"
"$work/lifecycle-smoke" -runtime wasm -guest "$work/chain.wasm" -bad-guest "$work/chain-bad.wasm" -worker "$work/worker"
printf 'Full platform acceptance passed.\n'
