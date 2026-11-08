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

# Run each Docker scenario in a separate shell so failures retain errexit behavior.
# Package and service commands are mocked: these tests never modify the host.
test_docker_install() {
    # shellcheck source=lib/common.sh
    source "$1/scripts/lib/common.sh"
    # shellcheck source=lib/host.sh
    source "$1/scripts/lib/host.sh"
    local scenario=$2 log=$3
    local docker_installed=0 compose_installed=0 docker_running=0
    local installed_packages='' service_failure=0 package_failure=0 repository_failure=0 broken_compose=0
    unset INSTALL_DOCKER
    NONINTERACTIVE=1
    case "$scenario" in
        fresh|repeat) ;;
        conflicts) docker_installed=1; installed_packages='docker.io docker-compose docker-compose-v2 docker-doc docker-buildx podman-docker containerd runc' ;;
        compose_missing) docker_installed=1; docker_running=1 ;;
        ready) docker_installed=1; compose_installed=1; docker_running=1; INSTALL_DOCKER=0 ;;
        stopped) docker_installed=1; compose_installed=1 ;;
        disabled) INSTALL_DOCKER=0 ;;
        service_failure) docker_installed=1; compose_installed=1; service_failure=1 ;;
        package_failure) package_failure=1 ;;
        repository_failure) repository_failure=1; installed_packages='docker.io' ;;
        broken_compose) broken_compose=1 ;;
        *) fail "Неизвестный сценарий: $scenario" ;;
    esac
    # shellcheck disable=SC2329 # Overrides the builtin in sourced installer code.
    command() {
        if [[ $* == '-v docker' ]]; then [[ $docker_installed == 1 ]]; else builtin command "$@"; fi
    }
    docker() {
        case "$*" in
            'compose version') [[ $compose_installed == 1 ]] ;;
            info) [[ $docker_running == 1 ]] ;;
            *) fail "Неожиданная команда Docker: $*" ;;
        esac
    }
    # shellcheck disable=SC2329 # Guard against reintroducing an interactive prompt.
    ask_yes() { fail 'Установка Docker запросила подтверждение.'; }
    configure_docker_repository() {
        printf 'repository\n' >>"$log"
        if (( repository_failure )); then return 1; fi
    }
    dpkg-query() {
        if [[ " $installed_packages " == *" $3 "* ]]; then
            printf 'install ok installed'
        else
            # Residual configuration files must not be treated as installed packages.
            printf 'deinstall ok config-files'
        fi
    }
    apt-get() {
        printf 'apt-get %s\n' "$*" >>"$log"
        if [[ $1 != update ]]; then
            [[ ${DEBIAN_FRONTEND:-} == noninteractive ]] || fail 'APT требует интерактивный ввод.'
        fi
        case "$*" in
            update|'install -y ca-certificates curl') ;;
            'remove -y --no-auto-remove '*)
                [[ $* == "remove -y --no-auto-remove $installed_packages" ]] || fail 'Удаляются лишние пакеты.'
                installed_packages=''
                ;;
            'install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin')
                [[ -z $installed_packages ]] || fail 'Конфликтующие пакеты не заменены.'
                if (( package_failure )); then return 1; fi
                docker_installed=1
                if (( ! broken_compose )); then compose_installed=1; fi
                ;;
            *) fail "Неожиданная команда APT: $*" ;;
        esac
    }
    systemctl() {
        printf 'systemctl %s\n' "$*" >>"$log"
        [[ $* == 'enable --now docker' ]] || fail 'Неожиданная команда systemctl.'
        if (( service_failure )); then return 1; fi
        docker_running=1
    }
    ensure_docker
    [[ $docker_installed == 1 && $compose_installed == 1 && $docker_running == 1 ]] || fail 'Docker не готов.'
    if [[ $scenario == repeat ]]; then ensure_docker; fi
}
export -f test_docker_install
for scenario in fresh conflicts compose_missing ready stopped repeat disabled service_failure package_failure repository_failure broken_compose; do
    log=$TEMP_DIR/docker-$scenario.log
    : >"$log"
    if bash -Eeuo pipefail -c 'test_docker_install "$@"' _ "$ROOT" "$scenario" "$log" >"$log.output" 2>&1; then
        case "$scenario" in
            disabled|*_failure|broken_compose) fail "Ошибочный сценарий Docker завершился успешно: $scenario" ;;
        esac
    else
        case "$scenario" in
            disabled|*_failure|broken_compose) ;;
            *) cat "$log.output" >&2; fail "Не пройден сценарий Docker: $scenario" ;;
        esac
    fi
    case "$scenario" in
        ready|disabled) [[ ! -s $log ]] || fail 'Готовый Docker или запрет установки вызвали изменения системы.' ;;
        stopped|service_failure) [[ $(cat "$log") == 'systemctl enable --now docker' ]] || fail 'Вместо запуска Docker переустанавливаются пакеты.' ;;
        fresh|conflicts|compose_missing|repeat)
            [[ $(grep -c '^apt-get install -y docker-ce ' "$log") == 1 ]] || fail 'Docker не установлен или переустановлен повторно.'
            grep -qx 'systemctl enable --now docker' "$log" || fail 'Не включён автозапуск Docker.'
            if [[ $scenario != conflicts ]] && grep -q '^apt-get remove ' "$log"; then fail 'Удаляются отсутствующие пакеты.'; fi
            ;;
        package_failure)
            if grep -q '^systemctl ' "$log"; then fail 'Установка продолжается после ошибки APT.'; fi
            ;;
        repository_failure)
            if grep -Eq '^apt-get (remove|install -y docker-ce)|^systemctl ' "$log"; then fail 'Пакеты изменены после ошибки репозитория.'; fi
            ;;
    esac
done
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
