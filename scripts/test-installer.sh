#!/usr/bin/env bash
set -Eeuo pipefail
ROOT=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
# shellcheck source=lib/common.sh
source "$ROOT/scripts/lib/common.sh"
# shellcheck source=lib/host.sh
source "$ROOT/scripts/lib/host.sh"
python3 "$ROOT/scripts/test-installer-archive.py"
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

# Exercise actual file backup/restore without stopping a host container.
test_release_upgrade() {
    # shellcheck source=lib/common.sh
    source "$1/scripts/lib/common.sh"
    # shellcheck source=lib/upgrade.sh
    source "$1/scripts/lib/upgrade.sh"
    INSTALL_DIR=$2/install SOURCE_DIR=$2/next
    local scenario=$3
    docker() {
        if [[ $* == info ]]; then return; fi
        [[ $1 == compose && $3 == "$INSTALL_DIR" && $5 == "$INSTALL_DIR/.env" ]] || fail 'Неверный проект Compose.'
        local compose_file=$7
        shift 7
        case "$*" in
            'exec -T xray /usr/local/bin/xray api statsquery --server=127.0.0.1:10085') ;;
            'build app')
                [[ $(<"$INSTALL_DIR/source/VERSION") == 0.2.0 && $(<"$SOURCE_DIR/VERSION") == 0.3.0 ]] || fail 'Неверный исходный код при сборке.'
                cmp -s "$compose_file" "$SOURCE_DIR/deploy/compose.yaml"
                ;;
            'stop app') ;;
            'up -d --no-build --no-deps --wait --wait-timeout 180 app')
                cmp -s "$compose_file" "$INSTALL_DIR/source/deploy/compose.yaml" || fail 'Версия Compose и код различаются.'
                if [[ $scenario == rollback && $(<"$INSTALL_DIR/source/VERSION") == 0.3.0 ]]; then
                    printf 'new migration\n' >>"$INSTALL_DIR/data/app.db"
                    printf 'new traffic\n' >"$INSTALL_DIR/data/traffic-pending.json"
                    return 1
                fi
                ;;
            'exec -T app /usr/local/bin/proxyk-admin healthcheck') ;;
            *) fail "Неожиданная команда обновления: $*" ;;
        esac
    }
    upgrade_release
}
export -f test_release_upgrade
for scenario in success rollback; do
    fixture=$TEMP_DIR/upgrade-$scenario
    install_dir=$fixture/install
    next_dir=$fixture/next
    mkdir -p "$install_dir"/{data,source/deploy,xray,tls} "$next_dir"/{cmd,internal,web,deploy,scripts}
    printf '0.2.0\n' >"$install_dir/source/VERSION"
    printf '0.3.0\n' >"$next_dir/VERSION"
    printf 'image: proxyk-admin:0.2.0\n' >"$install_dir/source/deploy/compose.yaml"
    printf 'image: proxyk-admin:0.3.0\n' >"$next_dir/deploy/compose.yaml"
    cp "$install_dir/source/deploy/compose.yaml" "$install_dir/compose.yaml"
    touch "$next_dir/go.mod" "$next_dir/go.sum" "$next_dir/.dockerignore" "$next_dir/scripts/install.sh"
    printf 'PUBLIC_IP=8.8.8.8\nADMIN_PORT=2053\nVPN_PORT=443\nTLS_MODE=acme\n' >"$install_dir/.env"
    printf 'previous database\n' >"$install_dir/data/app.db"
    printf 'previous WAL\n' >"$install_dir/data/app.db-wal"
    printf 'previous traffic\n' >"$install_dir/data/traffic-pending.json"
    printf 'existing Xray settings\n' >"$install_dir/xray/config.json"
    printf 'existing TLS certificate\n' >"$install_dir/tls/fullchain.pem"
    cp -a "$install_dir/data" "$fixture/original-data"
    sha256sum "$install_dir/.env" "$install_dir/xray/config.json" "$install_dir/tls/fullchain.pem" >"$fixture/settings.sha"
    if bash -Eeuo pipefail -c 'test_release_upgrade "$@"' _ "$ROOT" "$fixture" "$scenario" >"$fixture/output" 2>&1; then
        [[ $scenario == success && $(<"$install_dir/source/VERSION") == 0.3.0 ]] || fail 'Ошибка обновления не обнаружена.'
    else
        [[ $scenario == rollback && $(<"$install_dir/source/VERSION") == 0.2.0 ]] || { cat "$fixture/output" >&2; fail 'Обновление не удалось.'; }
        grep -qx 'Предыдущая версия восстановлена.' "$fixture/output" || { cat "$fixture/output" >&2; fail 'Старое приложение не запущено.'; }
    fi
    backup=("$install_dir/backups/from-0.2.0-"*)
    diff -r "$fixture/original-data" "${backup[0]}/data"
    diff -r "$fixture/original-data" "$install_dir/data"
    sha256sum --check --status "$fixture/settings.sha"
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
