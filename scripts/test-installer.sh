#!/usr/bin/env bash
set -Eeuo pipefail
ROOT=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
# shellcheck source=lib/common.sh
source "$ROOT/scripts/lib/common.sh"
# shellcheck source=lib/host.sh
source "$ROOT/scripts/lib/host.sh"
for port in 1 443 2053 65535; do valid_port "$port" || fail "Отклонён допустимый порт: $port"; done
for port in 0 -1 65536 01 2x ' ' '2053;echo bad'; do
    if valid_port "$port"; then fail "Принят недопустимый порт: $port"; fi
done
for ip in 8.8.8.8 1.1.1.1; do public_ipv4 "$ip" || fail 'Допустимый IPv4 отклонён'; done
for ip in '' 0.0.0.0 127.0.0.1 10.0.0.1 192.168.0.1 100.64.0.1 192.0.2.1 224.0.0.1 ::1 invalid; do
    if public_ipv4 "$ip"; then fail "Принят непубличный IPv4: $ip"; fi
done
python3 - "$ROOT" <<'PY'
import socket, subprocess, sys
with socket.socket() as listener:
    listener.bind(('0.0.0.0', 0))
    listener.listen()
    port = str(listener.getsockname()[1])
    result = subprocess.run(['bash', '-c', 'source "$1/scripts/lib/common.sh"; port_available "$2"', '_', sys.argv[1], port])
    assert result.returncode != 0, 'Занятый порт принят как свободный'
PY
TEMP_DIR=$(mktemp -d)
trap 'rm -rf -- "$TEMP_DIR"' EXIT
(
    INSTALL_DIR=$TEMP_DIR/first
    mkdir -p "$INSTALL_DIR"
    PUBLIC_IP=8.8.8.8 ADMIN_PORT=2053 NONINTERACTIVE=1 TLS_MODE=selfsigned
    ip() { printf '[{"addr_info":[{"local":"8.8.8.8"}]}]'; }
    port_available() { [[ $1 != 443 ]]; }
    choose_network
    [[ $VPN_PORT == 8443 ]] || fail 'Не выбран запасной VPN-порт.'
    old=$(sha256sum "$INSTALL_DIR/.env")
    choose_network
    [[ $(sha256sum "$INSTALL_DIR/.env") == "$old" ]] || fail 'Повторный запуск изменил настройки.'
    ADMIN_PORT=9999
    if (choose_network) >/dev/null 2>&1; then fail 'Повторный запуск изменил порт.'; fi
)
(
    INSTALL_DIR=$TEMP_DIR/conflict
    mkdir -p "$INSTALL_DIR"
    PUBLIC_IP=8.8.8.8 ADMIN_PORT=2053 NONINTERACTIVE=1
    ip() { printf '[{"addr_info":[{"local":"8.8.8.8"}]}]'; }
    port_available() { return 1; }
    if (choose_network) >/dev/null 2>&1; then fail 'Занятый порт не остановил установку.'; fi
)
for file in "$ROOT"/scripts/*.sh "$ROOT"/scripts/lib/*.sh; do bash -n "$file"; done
printf 'Проверки установщика пройдены. Это не заменяет установку на чистую VM.\n'
