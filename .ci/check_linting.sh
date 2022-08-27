#!/bin/sh

echo "### Running gofmt..."
files="$(gofmt -l .)"

if [ !  -z "$files" ]; then
	# run gofmt to print out the diff of what needs to be changed
	gofmt -d -e .
	exit 1
fi

echo "### Running staticcheck..."
# NOTE: most of the skipped tests are from the default, SA1019 skips the ioutil
# deprecation warning since some distros (Debian) are only on Go 1.15, which
# was before some ioutil functionality was moved into other modules (e.g. os.)
# See: https://staticcheck.io/docs/configuration
staticcheck -checks all,-ST1000,-ST1003,-ST1016,-ST1020,-ST1021,-ST1022,-ST1023,-SA1019 ./...
