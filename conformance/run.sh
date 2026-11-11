#!/usr/bin/env bash
set -euo pipefail
# Run from the Cordis module root. Builds real independently authored plugins.
work="$(mktemp -d "${TMPDIR:-/tmp}/cordis-conformance.XXXXXX")"
trap 'rm -rf "$work"' EXIT
printf 'Platform: '
go env GOOS GOARCH GOVERSION
go test -tags=integration -race ./... -timeout 10m
go build -o "$work/basic-host" ./examples/basic-host
go build -o "$work/lifecycle-smoke" ./conformance/cmd/lifecycle-smoke
go build -o "$work/stream-smoke" ./conformance/cmd/stream-smoke
(
  cd examples/plugins/counter
  go build -o "$work/counter" .
)
(
  cd examples/plugins/chain
  go build -o "$work/chain" .
  go build -ldflags '-X main.rejectInitialization=true' -o "$work/chain-bad" .
)
(
  cd examples/plugins/feed
  go build -o "$work/feed" .
)
"$work/basic-host" -native "$work/counter"
"$work/lifecycle-smoke" -guest "$work/chain" -bad-guest "$work/chain-bad"
"$work/stream-smoke" -guest "$work/feed"
printf 'Full platform acceptance passed.\n'
