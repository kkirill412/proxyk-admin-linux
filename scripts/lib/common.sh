#!/usr/bin/env bash

fail() { printf 'Ошибка: %s\n' "$*" >&2; exit 1; }
info() { printf '%s\n' "$*" >&2; }

ask_yes() {
    local answer
    if [[ ${NONINTERACTIVE:-0} == 1 ]]; then return 1; fi
    if ! { printf '%s [y/N]: ' "$1" >/dev/tty; } 2>/dev/null; then return 1; fi
    read -r answer </dev/tty || return 1
    [[ $answer == y || $answer == Y || $answer == yes ]]
}

valid_port() { [[ $1 =~ ^[1-9][0-9]{0,4}$ ]] && (( 10#$1 <= 65535 )); }

public_ipv4() {
    python3 - "$1" <<'PY'
import ipaddress, sys
try:
    ip = ipaddress.ip_address(sys.argv[1])
    sys.exit(0 if ip.version == 4 and ip.is_global and not ip.is_multicast else 1)
except ValueError:
    sys.exit(1)
PY
}

port_available() {
    # Docker can publish via NAT without a listening process: check mappings too.
    if command -v docker >/dev/null && docker info >/dev/null 2>&1; then
        if docker ps --format '{{.Ports}}' | grep -Eq "(^|[[:space:],:])${1}->"; then return 1; fi
    fi
    python3 - "$1" <<'PY'
import socket, sys
sockets = []
try:
    for family, host in [(socket.AF_INET, '0.0.0.0'), (socket.AF_INET6, '::')]:
        try:
            s = socket.socket(family, socket.SOCK_STREAM)
            if family == socket.AF_INET6: s.setsockopt(socket.IPPROTO_IPV6, socket.IPV6_V6ONLY, 1)
            s.bind((host, int(sys.argv[1])))
            sockets.append(s)
        except OSError as e:
            if family == socket.AF_INET6 and e.errno in (97, 99): continue
            raise
except OSError:
    sys.exit(1)
finally:
    for s in sockets: s.close()
PY
}

compose() { docker compose --project-directory "$INSTALL_DIR" --env-file "$INSTALL_DIR/.env" -f "$INSTALL_DIR/compose.yaml" "$@"; }

copy_source() {
    local source=$1 destination=$2
    tar -C "$source" --exclude='web/node_modules' --exclude='web/dist' \
        -cf - VERSION go.mod go.sum cmd internal web deploy scripts .dockerignore | tar -C "$destination" -xf -
    chmod -R go-rwx "$destination"
    chmod +x "$destination"/scripts/*.sh
}

load_settings() {
    # Do not execute the .env file as shell code.
    local key value
    PUBLIC_IP='' ADMIN_PORT='' VPN_PORT='' TLS_MODE=''
    while IFS='=' read -r key value; do
        case "$key" in
            PUBLIC_IP) PUBLIC_IP=$value ;;
            ADMIN_PORT) ADMIN_PORT=$value ;;
            VPN_PORT) VPN_PORT=$value ;;
            TLS_MODE) TLS_MODE=$value ;;
        esac
    done <"$INSTALL_DIR/.env"
    public_ipv4 "$PUBLIC_IP" || fail 'Некорректный PUBLIC_IP в .env.'
    if ! valid_port "$ADMIN_PORT" || ! valid_port "$VPN_PORT"; then fail 'Некорректный порт в .env.'; fi
    [[ $ADMIN_PORT != "$VPN_PORT" && $ADMIN_PORT != 80 && $VPN_PORT != 80 ]] || fail 'Порты конфликтуют.'
    [[ $TLS_MODE == acme || $TLS_MODE == selfsigned || $TLS_MODE == auto ]] || fail 'Некорректный TLS_MODE.'
}

save_settings() {
    local tmp
    tmp=$(mktemp "$INSTALL_DIR/.env.XXXXXX")
    printf 'PUBLIC_IP=%s\nADMIN_PORT=%s\nVPN_PORT=%s\nTLS_MODE=%s\n' "$PUBLIC_IP" "$ADMIN_PORT" "$VPN_PORT" "$TLS_MODE" >"$tmp"
    chmod 600 "$tmp"
    mv -f "$tmp" "$INSTALL_DIR/.env"
}
