#!/usr/bin/env bash
# Writes the licenses of everything compiled into the deckchat binary (the Go
# standard library and every linked module) to stdout. The release ships it as
# THIRD_PARTY_LICENSES.txt. The phone page has no runtime dependencies.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/../companion"

license_of() {
  for f in LICENSE LICENSE.txt LICENSE.md COPYING; do
    if [ -f "$1/$f" ]; then cat "$1/$f"; return; fi
  done
  echo "no license file found in $1" >&2
  exit 1
}

printf 'DeckChat bundles the following third-party software.\n'
printf '\n==== Go standard library (%s) ====\n\n' "$(go env GOVERSION)"
goroot=$(go env GOROOT)
# Homebrew keeps Go's LICENSE one level above GOROOT.
if [ -f "$goroot/LICENSE" ]; then license_of "$goroot"; else license_of "$goroot/.."; fi
GOOS=linux GOARCH=amd64 go list -deps -f '{{with .Module}}{{if not .Main}}{{.Path}} {{.Version}} {{.Dir}}{{end}}{{end}}' ./cmd/deckchat |
  sort -u | while read -r path version dir; do
    printf '\n==== %s %s ====\n\n' "$path" "$version"
    license_of "$dir"
  done
