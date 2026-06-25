#!/usr/bin/env bash
# Build a pinned Emacs from source for safeslop's hermetic Emacs tests.
#
# Hard rules (specs/0049):
#   - build/download a pinned Emacs version only
#   - verify SHA256 against ci/emacs32/emacs-32.1.tar.xz.sha256
#   - never `brew install emacs`, `apt install emacs`, emacs-snapshot, or `latest`
#
# This script fails closed: it refuses to run while the SHA file still carries
# the PENDING sentinel (Emacs 32.1 not yet GA).
set -euo pipefail

VERSION="${EMACS_VERSION:-32.1}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SHA_FILE="${HERE}/emacs-${VERSION}.tar.xz.sha256"
PREFIX="${EMACS_PREFIX:-${HOME}/.local/opt/emacs-${VERSION}}"
WORK="${EMACS_BUILD_DIR:-$(mktemp -d)}"
MIRROR="${EMACS_MIRROR:-https://ftp.gnu.org/gnu/emacs}"
TARBALL="emacs-${VERSION}.tar.xz"

if [ ! -f "${SHA_FILE}" ]; then
  echo "missing SHA file: ${SHA_FILE}" >&2
  exit 1
fi

EXPECTED_SHA="$(grep -v '^#' "${SHA_FILE}" | awk 'NF {print $1; exit}')"
if [ -z "${EXPECTED_SHA}" ] || [ "${EXPECTED_SHA}" = "PENDING_32_1_GA_REPLACE_WITH_REAL_SHA256" ]; then
  echo "Emacs ${VERSION} SHA256 is not pinned yet (sentinel present in ${SHA_FILE})." >&2
  echo "Fill in the real upstream SHA256 once emacs-${VERSION}.tar.xz is published." >&2
  exit 2
fi

mkdir -p "${WORK}"
cd "${WORK}"

if [ ! -f "${TARBALL}" ]; then
  echo "downloading ${MIRROR}/${TARBALL}"
  curl -fsSL -o "${TARBALL}" "${MIRROR}/${TARBALL}"
fi

echo "${EXPECTED_SHA}  ${TARBALL}" | shasum -a 256 -c -

tar -xf "${TARBALL}"
cd "emacs-${VERSION}"
./configure --prefix="${PREFIX}" --without-x --with-json
make -j"$(getconf _NPROCESSORS_ONLN 2>/dev/null || echo 2)"
make install

"${PREFIX}/bin/emacs" --batch --eval '(princ (format "built emacs %s\n" emacs-version))'
echo "EMACS=${PREFIX}/bin/emacs"
