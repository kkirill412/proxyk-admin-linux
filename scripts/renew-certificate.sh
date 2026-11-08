#!/usr/bin/env bash
set +x
set -Eeuo pipefail
umask 077
INSTALL_DIR=${1:-/opt/proxyk-admin}
SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=lib/common.sh
source "$SCRIPT_DIR/lib/common.sh"
# shellcheck source=lib/certificates.sh
source "$SCRIPT_DIR/lib/certificates.sh"
[[ $EUID == 0 ]] || fail 'Запустите от root.'
exec 9>/run/lock/proxyk-admin-install.lock
flock -w 60 9 || fail 'Другая операция уже выполняется.'
load_settings
if [[ $TLS_MODE == acme ]]; then
    port_available 80 || fail 'Порт 80 занят; продление не выполнено.'
    certbot_run renew --non-interactive --cert-name proxyk-admin --no-random-sleep-on-renew
    publish_certificate "$INSTALL_DIR/acme/live/proxyk-admin/fullchain.pem" "$INSTALL_DIR/acme/live/proxyk-admin/privkey.pem"
elif [[ $TLS_MODE == selfsigned ]]; then
    selfsigned_certificate
else
    fail 'Первоначальная настройка TLS ещё не завершена.'
fi
compose exec -T app /usr/local/bin/proxyk-admin healthcheck
openssl x509 -in "$INSTALL_DIR/tls/current/fullchain.pem" -noout -enddate -fingerprint -sha256
