#!/bin/sh
# Simple install script for conops-ctl

set -e

REPO="anuragxxd/conops"

# Function to get the latest tag from redirect
get_latest_release() {
  curl -Ls -o /dev/null -w %{url_effective} "https://github.com/$REPO/releases/latest" | rev | cut -d'/' -f1 | rev
}

echo "Finding latest release..."
VERSION=$(get_latest_release)

if [ -z "$VERSION" ]; then
    echo "Error: Unable to find latest release version."
    exit 1
fi

OS=$(uname -s)
ARCH=$(uname -m)

# Normalize OS
case "$OS" in
    Linux) OS="Linux" ;;
    Darwin) OS="Darwin" ;;
    *) echo "Unsupported OS: $OS"; exit 1 ;;
esac

# Normalize Arch
case "$ARCH" in
    x86_64) ARCH="x86_64" ;;
    arm64|aarch64) ARCH="arm64" ;;
    *) echo "Unsupported Architecture: $ARCH"; exit 1 ;;
esac

FILENAME="conops-ctl_${OS}_${ARCH}.tar.gz"
URL="https://github.com/$REPO/releases/download/$VERSION/$FILENAME"

echo "Downloading $VERSION for $OS/$ARCH..."
curl -sfL "$URL" -o "/tmp/$FILENAME"

echo "Extracting..."
tar -xzf "/tmp/$FILENAME" -C /tmp conops-ctl

echo "Installing to /usr/local/bin (requires sudo)..."
if [ -w /usr/local/bin ]; then
    mv "/tmp/conops-ctl" "/usr/local/bin/"
else
    sudo mv "/tmp/conops-ctl" "/usr/local/bin/"
fi
chmod +x /usr/local/bin/conops-ctl

rm "/tmp/$FILENAME"

echo "Successfully installed conops-ctl $VERSION!"
