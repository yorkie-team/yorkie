#!/usr/bin/env bash

set -e

YORKIE_VERSION=$1
GO_LDFLAGS=$2

if [ "$#" -ne 2 ]; then
  echo "Usage: ${0} VERSION GO_LDFLAGS" >> /dev/stderr
  exit 255
fi

set -u

function main {
  mkdir -p binaries
  cd binaries

  for os in darwin windows linux; do
    local ext=""

    export GOOS=${os}
    TARGET_ARCHS=("amd64")

    if [ "${GOOS}" == "windows" ]; then
      ext=".exe"
    fi

    if [ ${GOOS} == "linux" ]; then
      TARGET_ARCHS+=("arm64")
      TARGET_ARCHS+=("ppc64le")
    fi

    for TARGET_ARCH in "${TARGET_ARCHS[@]}"; do
      export GOARCH=${TARGET_ARCH}

      TARGET="yorkie-v${YORKIE_VERSION}-${GOOS}-${GOARCH}"
      mkdir -p "${TARGET}"

      CGO_ENABLED=0 go build -o "${TARGET}/yorkie${ext}" -ldflags "${GO_LDFLAGS}" ../cmd/yorkie

      for FILE_NAME in README ROADMAP CHANGELOG ; do
        cp "../${FILE_NAME}.md" "${TARGET}/${FILE_NAME}.md"
      done

      if [ ${GOOS} == "linux" ]; then
        tar cfz "${TARGET}.tar.gz" "${TARGET}"
        echo "Wrote binaries/${TARGET}.tar.gz"
      else
        zip -qr "${TARGET}.zip" "${TARGET}"
        echo "Wrote binaries/${TARGET}.zip"
      fi
    done
  done
}

main
