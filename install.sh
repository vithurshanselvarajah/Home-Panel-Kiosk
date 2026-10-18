#!/usr/bin/env bash
set -euo pipefail

REPOSITORY=${PITOUCH_REPOSITORY:-vithurshanselvarajah/Home-Panel-Kiosk}
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

# Resolve the per-user config directory used by the Electron app
APP_CONFIG_DIR=${XDG_CONFIG_HOME:-$HOME/.config}/homepanel-kiosk

# configure_pi_touch writes the initial settings.json so the app does not need
# to be configured from its UI. Values may be supplied via PITOUCH_* env vars
# (useful for automation) or gathered with read -p prompts.
configure_pi_touch() {
  local settings_file="${APP_CONFIG_DIR}/settings.json"
  local interactive=true
  if [[ -n "${PITOUCH_DASHBOARD_URL:-}${PITOUCH_SLIDESHOW_URL:-}${PITOUCH_MQTT_URL:-}${PITOUCH_MQTT_USERNAME:-}${PITOUCH_MQTT_PASSWORD:-}${PITOUCH_MQTT_PREFIX:-}${PITOUCH_DEVICE_NAME:-}${PITOUCH_IDLE_SLIDESHOW_MINUTES:-}${PITOUCH_SLIDESHOW_OFF_MINUTES:-}${PITOUCH_TOUCH_KEYBOARD:-}" ]]; then
    interactive=false
  fi

  if [[ "${PITOUCH_SKIP_SETUP:-0}" == "1" ]]; then
    echo "Skipping initial setup (PITOUCH_SKIP_SETUP=1). The app will open its settings UI on first launch."
    return 0
  fi

  # Validate a URL; accept http(s) for dashboard/slideshow and mqtt(s) for the broker.
  validate_url() {
    local value=$1 label=$2 scheme=$3
    if [[ -z "$value" ]]; then return 0; fi
    if [[ "$value" =~ ^${scheme}:// ]]; then return 0; fi
    echo "Invalid $label URL: '$value' (must start with ${scheme}://)" >&2
    return 1
  }

  # Pull defaults from any existing settings.json so re-running the installer
  # pre-fills the prompts with the user's previous values.
  local existing_dashboard_url="" existing_slideshow_url="" existing_mqtt_url=""
  local existing_mqtt_user="" existing_mqtt_prefix="" existing_device_name=""
  local existing_idle_slideshow_min="" existing_slideshow_off_min="" existing_touch_keyboard=""
  if [[ -f "$settings_file" ]] && command -v python3 >/dev/null 2>&1; then
    read_existing() {
      PITOUCH_SETTINGS_PATH="$settings_file" python3 - "$1" <<'PY'
import json, os, sys
key = sys.argv[1]
try:
    with open(os.environ["PITOUCH_SETTINGS_PATH"]) as fh:
        data = json.load(fh)
    value = data.get(key, "")
    if isinstance(value, bool):
        print("true" if value else "false")
    else:
        print(value)
except (OSError, ValueError):
    print("")
PY
    }
    existing_dashboard_url=$(read_existing dashboardUrl)
    existing_slideshow_url=$(read_existing slideshowUrl)
    existing_mqtt_url=$(read_existing mqttUrl)
    existing_mqtt_user=$(read_existing mqttUsername)
    existing_mqtt_prefix=$(read_existing mqttPrefix)
    existing_device_name=$(read_existing deviceName)
    existing_idle_slideshow_min=$(read_existing idleToSlideshowMinutes)
    existing_slideshow_off_min=$(read_existing slideshowToOffMinutes)
    existing_touch_keyboard=$(read_existing touchKeyboardEnabled)
  fi

  local dashboard_url slideshow_url mqtt_url mqtt_user mqtt_pass mqtt_prefix device_name
  local idle_slideshow_min slideshow_off_min touch_keyboard

  # Fall back to the documented defaults if no prior value is recorded.
  local def_dashboard=${existing_dashboard_url:-http://homeassistant.local:8123}
  local def_slideshow=${existing_slideshow_url:-}
  local def_mqtt=${existing_mqtt_url:-}
  local def_mqtt_user=${existing_mqtt_user:-}
  local def_mqtt_prefix=${existing_mqtt_prefix:-homepanel}
  local def_device_name=${existing_device_name:-Home Panel}
  local def_idle_slideshow_min=${existing_idle_slideshow_min:-5}
  local def_slideshow_off_min=${existing_slideshow_off_min:-15}
  local def_touch_keyboard=${existing_touch_keyboard:-true}

  if $interactive; then
    echo ""
    echo "PiTouch initial setup"
    echo "Press Enter to accept the default shown in [brackets]; leave fields blank to skip optional ones."
    echo "Set PITOUCH_SKIP_SETUP=1 to skip this step and configure later from the app UI."
    if [[ -f "$settings_file" ]]; then
      echo "Existing settings found at $settings_file — defaults are pre-filled from those."
    fi
    echo ""
    while true; do
      read -r -p "Home Assistant dashboard URL [$def_dashboard]: " dashboard_url
      dashboard_url=${dashboard_url:-$def_dashboard}
      validate_url "$dashboard_url" "dashboard" "https?" && break
    done
    if [[ -n "$def_slideshow" ]]; then
      read -r -p "Immich Kiosk URL (optional) [$def_slideshow]: " slideshow_url
    else
      read -r -p "Immich Kiosk URL (optional): " slideshow_url
    fi
    slideshow_url=${slideshow_url:-$def_slideshow}
    if [[ -n "$slideshow_url" ]]; then validate_url "$slideshow_url" "slideshow" "https?" || slideshow_url=""; fi
    read -r -p "Idle minutes before slideshow (0 disables) [$def_idle_slideshow_min]: " idle_slideshow_min
    idle_slideshow_min=${idle_slideshow_min:-$def_idle_slideshow_min}
    read -r -p "Slideshow minutes before screen off (0 disables) [$def_slideshow_off_min]: " slideshow_off_min
    slideshow_off_min=${slideshow_off_min:-$def_slideshow_off_min}
    local tk_prompt="[Y/n]"
    [[ "$def_touch_keyboard" == "false" ]] && tk_prompt="[y/N]"
    read -r -p "Enable automatic touch keyboard? $tk_prompt: " touch_keyboard_input
    if [[ -z "$touch_keyboard_input" ]]; then
      [[ "$def_touch_keyboard" == "false" ]] && touch_keyboard=false || touch_keyboard=true
    elif [[ "$touch_keyboard_input" =~ ^[Yy]$ ]]; then touch_keyboard=true; else touch_keyboard=false; fi
    if [[ -n "$def_mqtt" ]]; then
      read -r -p "MQTT broker URL (optional) [$def_mqtt]: " mqtt_url
    else
      read -r -p "MQTT broker URL (optional, e.g. mqtt://192.168.1.10:1883): " mqtt_url
    fi
    mqtt_url=${mqtt_url:-$def_mqtt}
    if [[ -n "$mqtt_url" ]] && ! validate_url "$mqtt_url" "MQTT" "mqtts?"; then mqtt_url=""; fi
    if [[ -n "$mqtt_url" ]]; then
      local user_prompt="MQTT username (optional)"
      [[ -n "$def_mqtt_user" ]] && user_prompt="MQTT username [$def_mqtt_user]"
      read -r -p "$user_prompt: " mqtt_user
      mqtt_user=${mqtt_user:-$def_mqtt_user}
      read -r -s -p "MQTT password (optional, input hidden — leave blank to keep existing): " mqtt_pass; echo ""
      read -r -p "MQTT topic prefix [$def_mqtt_prefix]: " mqtt_prefix
      mqtt_prefix=${mqtt_prefix:-$def_mqtt_prefix}
      local name_prompt="Device name shown in Home Assistant"
      [[ -n "$def_device_name" && "$def_device_name" != "Home Panel" ]] && name_prompt="Device name shown in Home Assistant [$def_device_name]"
      read -r -p "$name_prompt: " device_name
      device_name=${device_name:-$def_device_name}
    fi
  else
    dashboard_url=${PITOUCH_DASHBOARD_URL:-http://homeassistant.local:8123}
    slideshow_url=${PITOUCH_SLIDESHOW_URL:-}
    idle_slideshow_min=${PITOUCH_IDLE_SLIDESHOW_MINUTES:-5}
    slideshow_off_min=${PITOUCH_SLIDESHOW_OFF_MINUTES:-15}
    touch_keyboard=${PITOUCH_TOUCH_KEYBOARD:-true}
    mqtt_url=${PITOUCH_MQTT_URL:-}
    mqtt_user=${PITOUCH_MQTT_USERNAME:-}
    mqtt_pass=${PITOUCH_MQTT_PASSWORD:-}
    mqtt_prefix=${PITOUCH_MQTT_PREFIX:-homepanel}
    device_name=${PITOUCH_DEVICE_NAME:-Home Panel}
    validate_url "$dashboard_url" "dashboard" "https?" || { echo "PITOUCH_DASHBOARD_URL is invalid." >&2; exit 1; }
    [[ -z "$slideshow_url" ]] || validate_url "$slideshow_url" "slideshow" "https?" || { echo "PITOUCH_SLIDESHOW_URL is invalid." >&2; exit 1; }
    [[ -z "$mqtt_url" ]] || validate_url "$mqtt_url" "MQTT" "mqtts?" || { echo "PITOUCH_MQTT_URL is invalid." >&2; exit 1; }
  fi

  mkdir -p "$APP_CONFIG_DIR"

  # Build the JSON file using python3 (already a hard dependency for this script)
  # so values are escaped properly. The MQTT password is written in plaintext;
  # the app will encrypt it on next save via Electron safeStorage.
  PITOUCH_DASHBOARD_URL="$dashboard_url" \
  PITOUCH_SLIDESHOW_URL="$slideshow_url" \
  PITOUCH_IDLE_SLIDESHOW_MINUTES="$idle_slideshow_min" \
  PITOUCH_SLIDESHOW_OFF_MINUTES="$slideshow_off_min" \
  PITOUCH_TOUCH_KEYBOARD="$touch_keyboard" \
  PITOUCH_MQTT_URL="$mqtt_url" \
  PITOUCH_MQTT_USERNAME="$mqtt_user" \
  PITOUCH_MQTT_PASSWORD="$mqtt_pass" \
  PITOUCH_MQTT_PREFIX="$mqtt_prefix" \
  PITOUCH_DEVICE_NAME="$device_name" \
  python3 - "$settings_file" <<'PY'
import json, os, sys

def int_env(name, default):
    raw = os.environ.get(name, "").strip()
    if not raw:
        return default
    try:
        return int(raw)
    except ValueError:
        return default

out_path = sys.argv[1]
settings = {
    "configured": True,
    "dashboardUrl": os.environ["PITOUCH_DASHBOARD_URL"],
    "slideshowUrl": os.environ["PITOUCH_SLIDESHOW_URL"],
    "idleToSlideshowMinutes": int_env("PITOUCH_IDLE_SLIDESHOW_MINUTES", 5),
    "slideshowToOffMinutes": int_env("PITOUCH_SLIDESHOW_OFF_MINUTES", 15),
    "startInKiosk": True,
    "touchKeyboardEnabled": os.environ.get("PITOUCH_TOUCH_KEYBOARD", "true").lower() not in ("0", "false", "no"),
    "mqttUrl": os.environ.get("PITOUCH_MQTT_URL", ""),
    "mqttUsername": os.environ.get("PITOUCH_MQTT_USERNAME", ""),
    "mqttPassword": os.environ.get("PITOUCH_MQTT_PASSWORD", ""),
    "mqttPrefix": os.environ.get("PITOUCH_MQTT_PREFIX", "homepanel") or "homepanel",
    "deviceName": os.environ.get("PITOUCH_DEVICE_NAME", "Home Panel") or "Home Panel",
}

with open(out_path, "w", encoding="utf-8") as fh:
    json.dump(settings, fh, indent=2)
    fh.write("\n")
os.chmod(out_path, 0o600)
PY

  echo "Initial settings written to $settings_file."
}

configure_pi_touch

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
if [[ "${PITOUCH_SKIP_SETUP:-0}" == "1" ]]; then
  echo "Initial setup was skipped; open Settings from the side panel to configure the dashboard."
else
  echo "If you skipped setup, open Settings from the side panel to update the dashboard URL or MQTT settings."
fi
