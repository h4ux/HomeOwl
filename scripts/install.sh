#!/bin/sh
# HomeOwl installer for macOS and Linux.
#
#   curl -fsSL https://github.com/h4ux/HomeOwl/releases/latest/download/install.sh | sh
#
# Options (environment variables):
#   HOMEOWL_VERSION=v1.2.3   install a specific release (default: latest)
#   HOMEOWL_BIN_DIR=/path    where to put the program (default: /usr/local/bin, or ~/.local/bin without sudo)
#   HOMEOWL_NO_SERVICE=1     don't set up the background service
#   HOMEOWL_FILE=/path.tar.gz  install from a downloaded archive instead of GitHub
set -eu

REPO="h4ux/HomeOwl"
say() { printf '\033[1m%s\033[0m\n' "$*"; }
ok() { printf '  \033[32m✓\033[0m %s\n' "$*"; }
warn() { printf '  \033[33m!\033[0m %s\n' "$*"; }
die() { printf '  \033[31m✗ %s\033[0m\n' "$*" >&2; exit 1; }

OS=$(uname -s); ARCH=$(uname -m)
case "$OS" in Darwin) os=darwin ;; Linux) os=linux ;; *) die "Unsupported system: $OS (on Windows use install.ps1)" ;; esac
case "$ARCH" in x86_64|amd64) arch=amd64 ;; arm64|aarch64) arch=arm64 ;; *) die "Unsupported processor: $ARCH" ;; esac
command -v curl >/dev/null || die "curl is required"

SUDO=""
if [ "$(id -u)" -ne 0 ] && command -v sudo >/dev/null; then SUDO="sudo"; fi
BIN_DIR="${HOMEOWL_BIN_DIR:-/usr/local/bin}"
if [ -z "${HOMEOWL_BIN_DIR:-}" ] && [ -z "$SUDO" ] && [ "$(id -u)" -ne 0 ]; then BIN_DIR="$HOME/.local/bin"; SUDO=""; fi

say "Installing HomeOwl for $os/$arch"
TMP=$(mktemp -d); trap 'rm -rf "$TMP"' EXIT
name="homeowl_${os}_${arch}.tar.gz"
if [ -n "${HOMEOWL_FILE:-}" ]; then
  cp "$HOMEOWL_FILE" "$TMP/$name"
else
  if [ -n "${HOMEOWL_VERSION:-}" ]; then base="https://github.com/$REPO/releases/download/$HOMEOWL_VERSION"; else base="https://github.com/$REPO/releases/latest/download"; fi
  curl -fsSL "$base/$name" -o "$TMP/$name" || die "Download failed: $base/$name"
  if curl -fsSL "$base/SHA256SUMS" -o "$TMP/SHA256SUMS" 2>/dev/null; then
    want=$(grep " $name\$" "$TMP/SHA256SUMS" | cut -d' ' -f1)
    if command -v sha256sum >/dev/null; then got=$(sha256sum "$TMP/$name" | cut -d' ' -f1); else got=$(shasum -a 256 "$TMP/$name" | cut -d' ' -f1); fi
    [ -n "$want" ] && [ "$want" = "$got" ] || die "Checksum mismatch — the download is damaged or was tampered with"
    ok "Downloaded and verified"
  else
    warn "No checksum file found — skipping verification"
  fi
fi
tar xzf "$TMP/$name" -C "$TMP"

# ---- moving from the earlier Mac app (UniFi Audit / Big Mama)
if [ "$os" = darwin ]; then
  for label in local.big-mama local.unifi-audit; do
    if [ -f "$HOME/Library/LaunchAgents/$label.plist" ]; then
      launchctl bootout "gui/$(id -u)/$label" 2>/dev/null || true
      rm -f "$HOME/Library/LaunchAgents/$label.plist"
      ok "Stopped the old app ($label)"
    fi
  done
  pkill -f "Resources/app/server.py" 2>/dev/null || true
  for app in "/Applications/Big Mama.app" "/Applications/UniFi Audit.app"; do
    [ -d "$app" ] && rm -rf "$app" && ok "Removed $(basename "$app") (your settings and data are kept)"
  done
  # people and category rules from the old analytics database, in case the new engine can't open it
  for d in "HomeOwl" "Big Mama" "UniFi Audit"; do
    sup="$HOME/Library/Application Support/$d"
    if [ -x "$sup/venv/bin/python" ] && [ -f "$sup/analytics.duckdb" ] && [ ! -f "$sup/import-legacy.json" ]; then
      "$sup/venv/bin/python" - "$sup" <<'PY' 2>/dev/null && ok "Saved your people and category rules for the new version" || true
import sys, json, duckdb
d = sys.argv[1]
c = duckdb.connect(d + "/analytics.duckdb", read_only=True)
q = lambda s: [list(map(lambda v: v if isinstance(v, (str, int, float, type(None))) else str(v), r)) for r in c.execute(s).fetchall()]
out = {"people": q("SELECT id, name, color, note FROM people"), "person_devices": q("SELECT person_id, kind, key FROM person_devices"),
       "category_rules": q("SELECT pattern, category FROM category_rules")}
json.dump(out, open(d + "/import-legacy.json", "w"))
PY
      break
    fi
  done
fi

# ---- program
if [ ! -d "$BIN_DIR" ]; then $SUDO mkdir -p "$BIN_DIR" 2>/dev/null || mkdir -p "$BIN_DIR"; fi
if [ -w "$BIN_DIR" ]; then SUDO_BIN=""; else SUDO_BIN="$SUDO"; fi
$SUDO_BIN install -m 0755 "$TMP/homeowl" "$BIN_DIR/homeowl"
ok "Installed $BIN_DIR/homeowl ($("$BIN_DIR/homeowl" version))"
case ":$PATH:" in *":$BIN_DIR:"*) ;; *) warn "Add $BIN_DIR to your PATH to run 'homeowl' from a terminal" ;; esac

# ---- background service
if [ -n "${HOMEOWL_NO_SERVICE:-}" ]; then
  say "Done. Start it with: $BIN_DIR/homeowl"
  exit 0
fi
if [ "$os" = darwin ]; then
  "$BIN_DIR/homeowl" service stop >/dev/null 2>&1 || true
  "$BIN_DIR/homeowl" service uninstall >/dev/null 2>&1 || true
  "$BIN_DIR/homeowl" service install && "$BIN_DIR/homeowl" service start
  ok "Runs in the background and starts when you log in"
elif command -v systemctl >/dev/null && [ -d /run/systemd/system ]; then
  user="${SUDO_USER:-$(id -un)}"
  $SUDO "$BIN_DIR/homeowl" service stop >/dev/null 2>&1 || true
  $SUDO "$BIN_DIR/homeowl" service uninstall >/dev/null 2>&1 || true
  $SUDO "$BIN_DIR/homeowl" service install --user "$user" && $SUDO "$BIN_DIR/homeowl" service start
  ok "Installed as a systemd service running as $user (starts at boot)"
else
  warn "No systemd found — start HomeOwl yourself: $BIN_DIR/homeowl run --no-browser"
fi

sleep 2
url="http://127.0.0.1:8555"
say ""
say "HomeOwl is ready: $url"
echo "  • On this computer, open $url and create your admin account."
echo "  • From another device: https://<this computer's IP>:8555 (you'll need the setup code: homeowl setup-code)."
echo "  • macOS may ask to let HomeOwl find devices on your local network — click Allow."
[ "$os" = darwin ] && open "$url" 2>/dev/null || true
