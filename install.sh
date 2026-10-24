#!/bin/sh
# install.sh: install the latest sift release into ~/.local/bin.
# Usage: curl -sSL https://tyrantkhan.github.io/sift/install.sh | sh
set -eu

REPO="tyrantkhan/sift"
INSTALL_DIR="${HOME}/.local/bin"
LATEST="https://github.com/${REPO}/releases/latest/download"

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$OS" in
    linux | darwin) ;;
    *) echo "Unsupported OS: $OS (sift ships for macOS and Linux)" >&2; exit 1 ;;
esac

ARCH="$(uname -m)"
case "$ARCH" in
    x86_64 | amd64) ARCH="amd64" ;;
    arm64 | aarch64) ARCH="arm64" ;;
    *) echo "Unsupported architecture: $ARCH" >&2; exit 1 ;;
esac

TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT

# The latest release's checksums name its archives, so one download gives both the version and the
# checksum to verify against, without the GitHub API and its rate limit.
curl -fsSL "${LATEST}/checksums.txt" -o "${TMPDIR}/checksums.txt"
LINE="$(grep "  sift_[0-9][^ ]*_${OS}_${ARCH}\.tar\.gz\$" "${TMPDIR}/checksums.txt" || true)"
if [ -z "$LINE" ]; then
    echo "The latest release has no archive for ${OS}/${ARCH}" >&2
    exit 1
fi
ARCHIVE="${LINE##* }"
VERSION="$(echo "$ARCHIVE" | sed "s/^sift_\(.*\)_${OS}_${ARCH}\.tar\.gz\$/\1/")"

echo "Installing sift v${VERSION} (${OS}/${ARCH})..."
curl -fsSL "https://github.com/${REPO}/releases/download/v${VERSION}/${ARCHIVE}" -o "${TMPDIR}/${ARCHIVE}"

if command -v sha256sum >/dev/null 2>&1; then
    SUM="$(sha256sum "${TMPDIR}/${ARCHIVE}" | cut -d' ' -f1)"
else
    SUM="$(shasum -a 256 "${TMPDIR}/${ARCHIVE}" | cut -d' ' -f1)"
fi
if [ "$SUM" != "${LINE%% *}" ]; then
    echo "Checksum mismatch for ${ARCHIVE}; refusing to install it" >&2
    exit 1
fi

tar -xzf "${TMPDIR}/${ARCHIVE}" -C "$TMPDIR" sift
mkdir -p "$INSTALL_DIR"
mv "${TMPDIR}/sift" "${INSTALL_DIR}/sift"
chmod +x "${INSTALL_DIR}/sift"

echo "Installed sift ${VERSION} to ${INSTALL_DIR}/sift"

case ":${PATH}:" in
    *":${INSTALL_DIR}:"*)
        echo "Run 'sift help' to get started."
        ;;
    *)
        echo ""
        echo "WARNING: ${INSTALL_DIR} is not in your PATH, so the 'sift' command won't be found until you add it."
        echo ""
        SHELL_NAME="$(basename "${SHELL:-sh}")"
        case "$SHELL_NAME" in
            zsh) RC_FILE="${HOME}/.zshrc" ;;
            bash) RC_FILE="${HOME}/.bashrc" ;;
            *) RC_FILE="" ;;
        esac
        if [ "$SHELL_NAME" = "fish" ]; then
            echo "Run this to add it:"
            echo "  fish_add_path ${INSTALL_DIR}"
        elif [ -n "$RC_FILE" ]; then
            echo "Run this to add it:"
            echo "  echo 'export PATH=\"${INSTALL_DIR}:\$PATH\"' >> ${RC_FILE}"
        else
            echo "Add this to your shell's config file (e.g. ~/.profile):"
            echo "  export PATH=\"${INSTALL_DIR}:\$PATH\""
        fi
        echo ""
        echo "Then restart your shell or run: exec \$SHELL"
        ;;
esac
