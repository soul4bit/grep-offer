#!/usr/bin/env bash
set -euo pipefail

APP_NAME="${APP_NAME:-grep-offer}"
APP_ROOT="${APP_ROOT:-/var/www/grep-offer}"
APP_USER="${APP_USER:-www-data}"
APP_GROUP="${APP_GROUP:-www-data}"
RUNNER_USER="${RUNNER_USER:-${DEPLOY_USER:-$USER}}"

SCRIPT_DIR="$(cd -- "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SERVICE_TEMPLATE="$SCRIPT_DIR/systemd/${APP_NAME}.service.tmpl"

if [[ ! -f "$SERVICE_TEMPLATE" ]]; then
  echo "Service template not found: $SERVICE_TEMPLATE" >&2
  exit 1
fi

sudo install -d -m 755 "$APP_ROOT"
sudo install -d -m 755 "$APP_ROOT/releases"
sudo install -d -m 775 -o "$APP_USER" -g "$APP_GROUP" "$APP_ROOT/shared" "$APP_ROOT/shared/uploads" "$APP_ROOT/shared/content" "$APP_ROOT/shared/content/articles"
sudo install -d -m 775 -o "$RUNNER_USER" -g "$RUNNER_USER" "$APP_ROOT/shared/flags"
sudo chown "$RUNNER_USER":"$RUNNER_USER" "$APP_ROOT" "$APP_ROOT/releases"

SERVICE_TMP="$(mktemp)"
trap 'rm -f "$SERVICE_TMP"' EXIT

sed \
  -e "s|{{APP_NAME}}|$APP_NAME|g" \
  -e "s|{{APP_ROOT}}|$APP_ROOT|g" \
  -e "s|{{APP_USER}}|$APP_USER|g" \
  -e "s|{{APP_GROUP}}|$APP_GROUP|g" \
  "$SERVICE_TEMPLATE" > "$SERVICE_TMP"

sudo install -m 644 "$SERVICE_TMP" "/etc/systemd/system/${APP_NAME}.service"

if [[ ! -f "/etc/${APP_NAME}.env" ]]; then
  sudo install -m 640 "$SCRIPT_DIR/${APP_NAME}.env.example" "/etc/${APP_NAME}.env"
  echo "Created /etc/${APP_NAME}.env. Edit it before the first deploy."
fi

sudo systemctl daemon-reload
sudo systemctl enable "$APP_NAME"

cat <<EOF
Bootstrap finished.

Next steps:
1. Edit /etc/${APP_NAME}.env if your port, PostgreSQL DSN or content paths should differ.
2. Allow ${RUNNER_USER} to restart the service without a password:
   sudo visudo -f /etc/sudoers.d/${APP_NAME}
   ${RUNNER_USER} ALL=NOPASSWD: /usr/bin/systemctl restart ${APP_NAME}, /usr/bin/systemctl is-active ${APP_NAME}
3. Install and register a GitHub self-hosted runner on this Ubuntu server.
4. Make sure the runner labels include self-hosted, Linux and X64.
5. Push to main or run the deploy workflow manually.
EOF
