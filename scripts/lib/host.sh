#!/usr/bin/env bash

ensure_docker() {
    if command -v docker >/dev/null && docker compose version >/dev/null 2>&1; then
        docker info >/dev/null 2>&1 || fail 'Docker установлен, но недоступен. Запустите Docker и повторите установку.'
        return
    fi
    if [[ ${INSTALL_DOCKER:-0} != 1 ]] && ! ask_yes 'Установить Docker Engine и Compose Plugin из официального репозитория?'; then
        fail 'Нужны Docker Engine и Compose Plugin. Для автоматической установки задайте INSTALL_DOCKER=1.'
    fi
    local package
    for package in docker.io podman-docker containerd runc; do
        if dpkg-query -W -f='${Status}' "$package" 2>/dev/null | grep -q 'install ok installed'; then
            fail "Обнаружен пакет $package. Сначала устраните конфликт по инструкции Docker; существующие пакеты не удалены."
        fi
    done
    install -m 0755 -d /etc/apt/keyrings
    curl --proto '=https' --tlsv1.2 -fsSL "https://download.docker.com/linux/$ID/gpg" -o /etc/apt/keyrings/docker.asc
    chmod 644 /etc/apt/keyrings/docker.asc
    cat >/etc/apt/sources.list.d/docker.sources <<EOF
Types: deb
URIs: https://download.docker.com/linux/$ID
Suites: $VERSION_CODENAME
Components: stable
Architectures: $(dpkg --print-architecture)
Signed-By: /etc/apt/keyrings/docker.asc
EOF
    apt-get update
    DEBIAN_FRONTEND=noninteractive apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
    systemctl enable --now docker
    docker info >/dev/null
    docker compose version >/dev/null
}

choose_network() {
    if [[ -f $INSTALL_DIR/.env ]]; then
        local requested_port=${ADMIN_PORT:-} requested_ip=${PUBLIC_IP:-}
        load_settings
        [[ -z $requested_port || $requested_port == "$ADMIN_PORT" ]] || fail 'ADMIN_PORT отличается от сохранённого; повторная установка не меняет порт.'
        [[ -z $requested_ip || $requested_ip == "$PUBLIC_IP" ]] || fail 'PUBLIC_IP отличается от сохранённого адреса.'
        info 'Используются сохранённые IP и порты.'
        return
    fi
    if [[ -z ${PUBLIC_IP:-} ]]; then
        local endpoint candidate
        for endpoint in https://api.ipify.org https://ipv4.icanhazip.com; do
            candidate=$(curl --noproxy '*' -4 --proto '=https' --tlsv1.2 -fsS --connect-timeout 5 --max-time 10 "$endpoint" 2>/dev/null) || continue
            if public_ipv4 "$candidate"; then PUBLIC_IP=$candidate; break; fi
        done
    fi
    public_ipv4 "${PUBLIC_IP:-}" || fail 'Не удалось определить публичный IPv4. Повторите с PUBLIC_IP=<публичный IPv4 сервера>.'
    # An outbound NAT address is not proof that this server owns the public IP.
    if ! ip -j -4 address show | python3 -c 'import json,sys; ip=sys.argv[1]; sys.exit(0 if any(a.get("local")==ip for i in json.load(sys.stdin) for a in i.get("addr_info",[])) else 1)' "$PUBLIC_IP"; then
        [[ ${ALLOW_NAT:-0} == 1 ]] || fail 'Публичный IPv4 не назначен интерфейсу. Для VPS за NAT настройте проброс портов и явно задайте PUBLIC_IP и ALLOW_NAT=1.'
    fi
    if [[ -z ${ADMIN_PORT:-} ]]; then
        [[ ${NONINTERACTIVE:-0} != 1 ]] || fail 'В неинтерактивном режиме задайте ADMIN_PORT (обычно 2053).'
        { printf 'Порт HTTPS-панели [2053]: ' >/dev/tty; } 2>/dev/null || fail 'Нет терминала. Задайте ADMIN_PORT=2053.'
        read -r ADMIN_PORT </dev/tty || fail 'Не удалось прочитать порт.'
        ADMIN_PORT=${ADMIN_PORT:-2053}
    fi
    valid_port "$ADMIN_PORT" || fail 'Порт должен быть целым числом от 1 до 65535.'
    [[ $ADMIN_PORT != 80 ]] || fail 'Порт 80 зарезервирован для проверки сертификатов.'
    port_available "$ADMIN_PORT" || fail "Порт панели $ADMIN_PORT занят. Выберите другой ADMIN_PORT."
    VPN_PORT=443
    if [[ $ADMIN_PORT == 443 ]] || ! port_available 443; then VPN_PORT=8443; fi
    if [[ $ADMIN_PORT == "$VPN_PORT" ]] || ! port_available "$VPN_PORT"; then
        fail 'Порты VPN 443 и 8443 недоступны. Освободите один из них.'
    fi
    TLS_MODE=${TLS_MODE:-auto}
    [[ $TLS_MODE == auto || $TLS_MODE == acme || $TLS_MODE == selfsigned ]] || fail 'TLS_MODE: auto, acme или selfsigned.'
    save_settings
}
