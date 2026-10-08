#!/usr/bin/env bash
# Build spor from this checkout and install it as a systemd service.
# Run as root on the server (tested on AlmaLinux 9/10):  sudo deploy/install.sh
set -euo pipefail
cd "$(dirname "$0")/.."

if [ "$(id -u)" -ne 0 ]; then
	echo "install.sh writes to /usr/local/bin, /etc/spor and /etc/systemd; run it as root:" >&2
	echo "  sudo GO=\"\$(command -v go)\" $0" >&2
	exit 1
fi

GO=${GO:-go}
if ! command -v "$GO" >/dev/null; then
	echo "go not found in root's PATH; install Go >= 1.25 or pass it: sudo GO=/path/to/go $0" >&2
	exit 1
fi

# safe.directory: as root, git refuses to read a checkout owned by another user.
ver=$(git -c safe.directory="$PWD" describe --tags --always --dirty 2>/dev/null || echo dev)
echo "building spor $ver"
CGO_ENABLED=0 "$GO" build -trimpath -ldflags "-s -w -X main.version=$ver" -o spor .
install -m 0755 spor /usr/local/bin/spor
rm -f spor
command -v restorecon >/dev/null && restorecon /usr/local/bin/spor || true

install -d -m 0755 /etc/spor /srv/spor/library
[ -e /etc/spor/spor.toml ] || install -m 0644 deploy/spor.toml /etc/spor/spor.toml
install -m 0644 deploy/spor.service /etc/systemd/system/spor.service
systemctl daemon-reload

if [ ! -e /etc/spor/spor.pub ]; then
	echo
	echo "Copy your public key (from 'spor keygen' on the workstation) to /etc/spor/spor.pub,"
	echo "then: systemctl enable --now spor"
	exit 0
fi
systemctl enable spor >/dev/null
systemctl restart spor
systemctl --no-pager --lines=5 status spor
