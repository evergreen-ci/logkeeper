#!/usr/bin/env bash

set -o errexit
set -o xtrace
set -o pipefail

rm -rf vendor/

make vendor
make vendor-sync
