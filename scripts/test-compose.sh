#!/usr/bin/env bash
set -Eeuo pipefail
umask 077
ROOT=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
[[ $EUID == 0 ]] || { echo 'Запустите sudo bash scripts/test-compose.sh: нужны права на каталоги UID 65532.' >&2; exit 1; }
docker info >/dev/null
INSTALL_DIR=$(mktemp -d /tmp/proxyk-admin-smoke-XXXXXXXX)
# shellcheck source=lib/common.sh
source "$ROOT/scripts/lib/common.sh"
# shellcheck source=lib/certificates.sh
source "$ROOT/scripts/lib/certificates.sh"
cleanup() {
    if [[ ${KEEP_SMOKE:-0} == 1 ]]; then printf 'Диагностическая установка сохранена: %s\n' "$INSTALL_DIR"; return; fi
    compose down --volumes >/dev/null 2>&1 || true
    rm -rf -- "$INSTALL_DIR"
}
trap cleanup EXIT
cp "$ROOT/deploy/compose.yaml" "$INSTALL_DIR/compose.yaml"
# Isolate Compose resources from an existing installation.
sed -i "s/^name: proxyk-admin$/name: proxyk-smoke-$$/" "$INSTALL_DIR/compose.yaml"
PUBLIC_IP=8.8.8.8 ADMIN_PORT=${SMOKE_ADMIN_PORT:-32053} VPN_PORT=${SMOKE_VPN_PORT:-32443} TLS_MODE=selfsigned
SMOKE_HOST=${SMOKE_HOST:-127.0.0.1}
if ! port_available "$ADMIN_PORT" || ! port_available "$VPN_PORT"; then fail 'Порты smoke-теста заняты.'; fi
save_settings
printf 'SOURCE_DIR=%s\n' "$ROOT" >>"$INSTALL_DIR/.env"
install -d -m 700 -o 65532 -g 65532 "$INSTALL_DIR/data" "$INSTALL_DIR/xray" "$INSTALL_DIR/tls"
selfsigned_certificate
compose config --quiet
compose build app
compose pull xray
compose run --rm --no-deps init
compose run --rm --no-deps xray run -test -config /etc/proxyk-admin/xray/config.json
compose up -d --wait --wait-timeout 180 app xray
compose exec -T app /usr/local/bin/proxyk-admin healthcheck
curl --noproxy '*' -fsS --cacert "$INSTALL_DIR/tls/current/fullchain.pem" \
    --connect-to "$PUBLIC_IP:$ADMIN_PORT:$SMOKE_HOST:$ADMIN_PORT" "https://$PUBLIC_IP:$ADMIN_PORT/health/ready"
python3 - "$VPN_PORT" "$SMOKE_HOST" <<'PY'
import socket, sys
with socket.create_connection((sys.argv[2], int(sys.argv[1])), timeout=5): pass
PY
if compose port xray 10085 >/dev/null 2>&1; then fail 'Управляющий API опубликован наружу.'; fi
config_before=$(sha256sum "$INSTALL_DIR/xray/config.json")
credentials_before=$(sha256sum "$INSTALL_DIR/data/initial-credentials.json")
compose restart app xray
compose up -d --wait --wait-timeout 180 app xray
compose run --rm --no-deps init
[[ $(sha256sum "$INSTALL_DIR/xray/config.json") == "$config_before" ]] || fail 'Ключи изменились.'
[[ $(sha256sum "$INSTALL_DIR/data/initial-credentials.json") == "$credentials_before" ]] || fail 'Администратор изменился.'
compose stop xray
if compose exec -T app /usr/local/bin/proxyk-admin healthcheck >/dev/null 2>&1; then fail 'Ложная готовность при недоступном Xray.'; fi
compose up -d --wait --wait-timeout 180 app xray
compose exec -T app /usr/local/bin/proxyk-admin healthcheck
compose logs --no-color >"$INSTALL_DIR/service-logs.txt"
python3 - "$INSTALL_DIR" <<'PY'
import json, pathlib, sys
root = pathlib.Path(sys.argv[1])
credentials = json.loads((root / 'data/initial-credentials.json').read_text())
logs = (root / 'service-logs.txt').read_text()
assert all(secret not in logs for secret in credentials.values()), 'Учётные данные попали в журнал'
PY
printf '\nDocker Compose smoke test пройден.\n'
