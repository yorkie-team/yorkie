#!/usr/bin/env bash

set -e

YORKIE_VERSION=$1

if [ -z "$1" ]; then
  echo "Usage: ${0} VERSION" >> /dev/stderr
  exit 255
fi

set -u

function main {
  mkdir -p release
  cd release

  tarcmd=tar

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

      go build -o "${TARGET}/yorkie${ext}" -ldflags "" ../cmd/yorkie

      for FILE_NAME in README ROADMAP CHANGELOG ; do
        cp "../${FILE_NAME}.md" "${TARGET}/${FILE_NAME}.md"
      done

      if [ ${GOOS} == "linux" ]; then
        ${tarcmd} cfz "${TARGET}.tar.gz" "${TARGET}"
        echo "Wrote release/${TARGET}.tar.gz"
      else
        zip -qr "${TARGET}.zip" "${TARGET}"
        echo "Wrote release/${TARGET}.zip"
      fi
    done
  done
}

main
