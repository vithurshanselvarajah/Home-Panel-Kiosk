#!/usr/bin/env bash
set -euo pipefail

REPOSITORY=${PITOUCH_REPOSITORY:-vithurshanselvarajah/pitouch}
if [[ -z "$REPOSITORY" ]] && command -v git >/dev/null 2>&1; then
  REMOTE=$(git config --get remote.origin.url 2>/dev/null || true)
  if [[ "$REMOTE" =~ github\.com[:/]([^/]+/[^/.]+)(\.git)?$ ]]; then
    REPOSITORY=${BASH_REMATCH[1]}
  fi
fi
if [[ ! "$REPOSITORY" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]]; then
  echo "Set PITOUCH_REPOSITORY to the GitHub owner/repository (for example owner/PiTouch)." >&2
  exit 1
fi

BITS=$(getconf LONG_BIT)
case "$(uname -m)" in
  x86_64) DEB_ARCH=amd64 ;;
  aarch64) DEB_ARCH=arm64 ;;
  *) echo "Unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac
[[ "$BITS" == 64 ]] || { echo "PiTouch requires a 64-bit operating system." >&2; exit 1; }

command -v wget >/dev/null 2>&1 || { echo "wget is required." >&2; exit 1; }
command -v python3 >/dev/null 2>&1 || { echo "python3 is required to read the GitHub release metadata." >&2; exit 1; }
command -v apt-get >/dev/null 2>&1 || { echo "This installer requires Debian or Raspberry Pi OS with apt-get." >&2; exit 1; }
command -v systemctl >/dev/null 2>&1 || { echo "systemd is required." >&2; exit 1; }

echo "Finding the latest PiTouch release for $DEB_ARCH..."
API_URL="https://api.github.com/repos/$REPOSITORY/releases/latest"
ASSET_URL=$(wget -qO- "$API_URL" | python3 -c '
import json, sys
arch = sys.argv[1]
release = json.load(sys.stdin)
assets = [a["browser_download_url"] for a in release.get("assets", [])
          if a.get("name", "").endswith(f"_{arch}.deb")]
if not assets:
    raise SystemExit(f"No .deb asset ending in _{arch}.deb was found in the latest stable release.")
print(assets[0])
' "$DEB_ARCH")

TMP_DIR=$(mktemp -d)
trap 'rm -rf "$TMP_DIR"' EXIT
DEB_FILE="$TMP_DIR/$(basename "$ASSET_URL")"
echo "Downloading $(basename "$DEB_FILE")..."
wget --show-progress -q -O "$DEB_FILE" "$ASSET_URL"

echo "Installing PiTouch..."
sudo apt-get install -y "$DEB_FILE"

DISPLAY_VALUE=${DISPLAY:-:0}
WAYLAND_VALUE=${WAYLAND_DISPLAY:-}
if [[ -z "$WAYLAND_VALUE" && -n "${XDG_RUNTIME_DIR:-}" && -S "$XDG_RUNTIME_DIR/wayland-0" ]]; then
  WAYLAND_VALUE=wayland-0
fi

SERVICE_DIR="$HOME/.config/systemd/user"
SERVICE_FILE="$SERVICE_DIR/homepanel-kiosk.service"
mkdir -p "$SERVICE_DIR"
{
  cat <<'UNIT'
[Unit]
Description=Home Panel Kiosk
After=graphical-session.target network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/bin/homepanel-kiosk
Restart=on-failure
RestartSec=5
UNIT
  printf 'Environment="DISPLAY=%s"\n' "$DISPLAY_VALUE"
  if [[ -n "$WAYLAND_VALUE" ]]; then printf 'Environment="WAYLAND_DISPLAY=%s"\n' "$WAYLAND_VALUE"; fi
  if [[ -n "${XDG_RUNTIME_DIR:-}" ]]; then printf 'Environment="XDG_RUNTIME_DIR=%s"\n' "$XDG_RUNTIME_DIR"; fi
  cat <<'UNIT'

[Install]
WantedBy=default.target
UNIT
} > "$SERVICE_FILE"

systemctl --user daemon-reload
if systemctl --user --quiet is-active homepanel-kiosk.service; then
  systemctl --user restart homepanel-kiosk.service
else
  systemctl --user enable --now homepanel-kiosk.service
fi

echo "PiTouch is installed and its user service is running."
echo "The first launch opens the app settings so you can enter your Home Assistant URL."
