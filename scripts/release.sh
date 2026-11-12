#!/bin/sh
# Build the release binaries into out/, with checksums.
#
#   scripts/release.sh            all targets
#   scripts/release.sh carplayd   one component
#
# Runs on the build host inside rootless podman, so the artifacts do
# not depend on anything installed there. Everything is CGO_ENABLED=0,
# -trimpath and stripped: the Pi has no libc to match and the binary is copied
# over the network often enough for 3 MB to matter.
#
# This exists because the first release was built by hand. The bytes that ended
# up on the garage box could not be reproduced from the tree, which makes
# "which build is running?" unanswerable the moment there are two of them.
set -eu
cd "$(dirname "$0")/.."
REPO=$(pwd)
CACHE=${CACHE:-$HOME/builds/.cache}
IMAGE=${IMAGE:-docker.io/library/golang:1.26}
LDFLAGS='-s -w'
mkdir -p out "$CACHE/go" "$CACHE/gomod"

# build <module dir> <binary name> <goarch>...
build() {
	dir=$1 name=$2
	shift 2
	for arch in "$@"; do
		echo "  $name-linux-$arch"
		podman run --rm -v "$REPO:/work:Z" -v "$CACHE/go:/gocache:z" -v "$CACHE/gomod:/gomod:z" \
			-e GOCACHE=/gocache -e GOMODCACHE=/gomod -e CGO_ENABLED=0 -e GOOS=linux -e GOARCH="$arch" \
			-w "/work/$dir" "$IMAGE" \
			go build -trimpath -ldflags "$LDFLAGS" -o "/work/out/$name-linux-$arch" .
	done
}

# test <module dir> -- vet and the full suite with the race detector, on the
# host's own architecture. A release is never cut from a tree that fails here.
test_mod() {
	echo "testing $1"
	podman run --rm -v "$REPO:/work:Z" -v "$CACHE/go:/gocache:z" -v "$CACHE/gomod:/gomod:z" \
		-e GOCACHE=/gocache -e GOMODCACHE=/gomod -w "/work/$1" "$IMAGE" \
		sh -c 'test -z "$(gofmt -l .)" || { echo "gofmt:"; gofmt -l .; exit 1; }; go vet ./... && go test -race ./...'
}

what=${1:-all}
for c in carplayd carrecon carnetbridge; do
	[ "$what" = all ] || [ "$what" = "$c" ] || continue
	case $c in
	carplayd) dir=carplayd; archs="amd64 arm64" ;;
	carrecon) dir=recon/carrecon; archs="amd64 arm64" ;;
	carnetbridge) dir=recon/carnetbridge; archs="amd64 arm64" ;;
	esac
	test_mod "$dir"
	echo "building $c"
	build "$dir" "$c" $archs
done

cd out
sha256sum ./*-linux-* > SHA256SUMS
echo
cat SHA256SUMS
