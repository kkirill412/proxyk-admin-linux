#!/usr/bin/env bash
set +x
set -Eeuo pipefail
umask 077

# pack-release.sh fills these values for a distributable curl | bash installer.
RELEASE_SOURCE_URL='https://github.com/kkirill412/proxyk-admin-linux/releases/download/v0.2.0/proxyk-admin-0.2.0.tar.gz'
RELEASE_SOURCE_SHA256='8de7fb1464a77c2e4f882a06810ac2496144a5b1ea4e99823ac2224ef87c3284'
INSTALL_DIR=${INSTALL_DIR:-/opt/proxyk-admin}
SOURCE_DIR=${SOURCE_DIR:-}
TEMP_SOURCE=''

die() { printf 'Ошибка: %s\n' "$*" >&2; exit 1; }
cleanup() { if [[ -n $TEMP_SOURCE ]]; then rm -rf -- "$TEMP_SOURCE"; fi; }
trap cleanup EXIT
trap 'printf "Установка не завершена (строка %s). Данные сохранены; устраните ошибку и повторите запуск.\n" "$LINENO" >&2' ERR

check_host() {
    [[ $(uname -s) == Linux ]] || die 'Поддерживается только Linux.'
    [[ $EUID == 0 ]] || die 'Запустите установщик через sudo bash.'
    [[ -f /etc/os-release ]] || die 'Не найден /etc/os-release.'
    # shellcheck source=/dev/null
    source /etc/os-release
    case "$ID:${VERSION_ID:-}" in
        ubuntu:22.04|ubuntu:24.04|ubuntu:26.04|ubuntu:26.10|debian:12|debian:13) ;;
        *) die 'Поддерживаются Ubuntu 22.04/24.04/26.04/26.10 и Debian 12/13.' ;;
    esac
    case "$(uname -m)" in x86_64|aarch64) ;; *) die 'Поддерживаются amd64 и arm64.' ;; esac
    [[ -d /run/systemd/system ]] || die 'Нужен сервер с systemd; установка внутри контейнера не поддерживается.'
    [[ $INSTALL_DIR =~ ^/opt/[a-zA-Z0-9_-]+$ ]] || die 'INSTALL_DIR должен иметь вид /opt/имя-приложения.'
    local tool missing=0
    for tool in curl openssl python3 ip flock tar sha256sum; do command -v "$tool" >/dev/null || missing=1; done
    if (( missing )); then
        apt-get update
        DEBIAN_FRONTEND=noninteractive apt-get install -y ca-certificates curl openssl python3 iproute2 util-linux tar
    fi
    exec 9>/run/lock/proxyk-admin-install.lock
    flock -n 9 || die 'Установщик или продление сертификата уже запущены.'
}

find_source() {
    if [[ -z $SOURCE_DIR && -n ${BASH_SOURCE[0]:-} && -f ${BASH_SOURCE[0]} ]]; then
        local candidate
        candidate=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
        if [[ -f $candidate/go.mod ]]; then SOURCE_DIR=$candidate; fi
    fi
    if [[ -z $SOURCE_DIR ]]; then
        local url=${SOURCE_URL:-$RELEASE_SOURCE_URL} sha=${SOURCE_SHA256:-$RELEASE_SOURCE_SHA256}
        [[ $url == https://* && $sha =~ ^[a-fA-F0-9]{64}$ ]] || die 'Для curl | bash нужен выпущенный установщик либо SOURCE_URL (HTTPS) и SOURCE_SHA256. Из клона: sudo bash scripts/install.sh.'
        TEMP_SOURCE=$(mktemp -d)
        curl --proto '=https' --proto-redir '=https' --tlsv1.2 -fSL --retry 3 --connect-timeout 15 --max-time 300 "$url" -o "$TEMP_SOURCE/source.tar.gz"
        printf '%s  %s\n' "$sha" "$TEMP_SOURCE/source.tar.gz" | sha256sum --check --status || die 'SHA-256 архива не совпадает.'
        # Extract regular files/directories only; reject symlinks and traversal.
        python3 - "$TEMP_SOURCE" <<'PY'
import pathlib, shutil, sys, tarfile
root = pathlib.Path(sys.argv[1])
with tarfile.open(root / 'source.tar.gz') as archive:
    members = archive.getmembers()
    for member in members:
        path = pathlib.PurePosixPath(member.name)
        if path.is_absolute() or '..' in path.parts or not (member.isfile() or member.isdir()):
            raise SystemExit('Небезопасный путь в архиве')
    # Copy only validated file contents: older Python has no extraction filters.
    # Use a fresh private directory and never restore archive owners or special bits.
    destination = root / 'source'
    destination.mkdir(mode=0o700)
    for member in members:
        target = destination / member.name
        if member.isdir():
            target.mkdir(mode=0o700, parents=True, exist_ok=True)
        else:
            target.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
            with archive.extractfile(member) as source, target.open('xb') as output:
                shutil.copyfileobj(source, output)
            target.chmod(0o700 if member.mode & 0o100 else 0o600)
PY
        SOURCE_DIR=$TEMP_SOURCE/source
    fi
    SOURCE_DIR=$(realpath "$SOURCE_DIR")
    [[ -f $SOURCE_DIR/go.mod && -f $SOURCE_DIR/VERSION && -f $SOURCE_DIR/deploy/compose.yaml && -f $SOURCE_DIR/scripts/lib/common.sh ]] || die 'Неполный архив исходного проекта.'
}

install_source() {
    if [[ -f $INSTALL_DIR/source/VERSION ]]; then
        cmp -s "$SOURCE_DIR/VERSION" "$INSTALL_DIR/source/VERSION" || die 'Уже установлена другая версия. Для перехода с 0.1.2 на 0.2.0 запустите установщик с --upgrade.'
        return
    fi
    local pending
    pending=$(mktemp -d "$INSTALL_DIR/.source-XXXXXXXX")
    copy_source "$SOURCE_DIR" "$pending"
    mv "$pending" "$INSTALL_DIR/source"
}

show_result() {
    printf '\nHTTPS: https://%s:%s\nVPN-порт: %s/TCP\n' "$PUBLIC_IP" "$ADMIN_PORT" "$VPN_PORT"
    compose ps
    if [[ $TLS_MODE == selfsigned ]]; then
        printf '\nВНИМАНИЕ: самоподписанный сертификат. Сверьте SHA-256 fingerprint в браузере перед входом:\n'
        openssl x509 -in "$INSTALL_DIR/tls/current/fullchain.pem" -noout -fingerprint -sha256
    fi
    local credentials=$INSTALL_DIR/data/initial-credentials.json
    if [[ -f $credentials ]]; then
        if [[ ${NONINTERACTIVE:-0} != 1 ]] && { true >/dev/tty; } 2>/dev/null; then
            python3 - "$credentials" <<'PY'
import json, sys
with open(sys.argv[1]) as source: credentials = json.load(source)
with open('/dev/tty', 'w') as tty:
    tty.write('\nЛогин: ' + credentials['username'] + '\nПароль: ' + credentials['password'] + '\nСохраните данные входа.\n')
PY
            rm -- "$credentials"
        else
            install -m 600 -o root -g root "$credentials" "$INSTALL_DIR/initial-credentials.json"
            rm -- "$credentials"
            printf 'Данные входа: %s/initial-credentials.json (только root; сохраните и удалите файл).\n' "$INSTALL_DIR"
        fi
    else
        printf 'Существующий администратор сохранён; пароль не изменялся.\n'
    fi
    printf 'Инфраструктура доступна. HTTPS и состояние Xray проверены.\n'
}

main() {
    if (( $# > 1 )) || [[ -n ${1:-} && $1 != --upgrade ]]; then
        die 'Использование: bash install.sh [--upgrade]'
    fi
    check_host
    find_source
    # shellcheck source=lib/common.sh
    source "$SOURCE_DIR/scripts/lib/common.sh"
    # shellcheck source=lib/host.sh
    source "$SOURCE_DIR/scripts/lib/host.sh"
    # shellcheck source=lib/certificates.sh
    source "$SOURCE_DIR/scripts/lib/certificates.sh"
    if [[ ${1:-} == --upgrade ]]; then
        # shellcheck source=lib/upgrade.sh
        source "$SOURCE_DIR/scripts/lib/upgrade.sh"
        upgrade_stage_two
        return
    fi
    ensure_docker
    install -d -m 700 "$INSTALL_DIR"
    choose_network
    install_source
    install -d -m 700 -o 65532 -g 65532 "$INSTALL_DIR/data" "$INSTALL_DIR/xray" "$INSTALL_DIR/tls"
    install -d -m 700 "$INSTALL_DIR/acme" "$INSTALL_DIR/acme-logs"
    if [[ ! -f $INSTALL_DIR/compose.yaml ]]; then install -m 600 "$INSTALL_DIR/source/deploy/compose.yaml" "$INSTALL_DIR/compose.yaml"; fi
    # Build using the installed copy, regardless of caller environment.
    unset SOURCE_DIR
    compose config --quiet
    compose build app
    compose pull xray
    compose run --rm --no-deps --build init
    obtain_certificate
    compose run --rm --no-deps xray run -test -config /etc/proxyk-admin/xray/config.json
    compose up -d --wait --wait-timeout 180 app xray
    compose exec -T app /usr/local/bin/proxyk-admin healthcheck
    compose exec -T xray /usr/local/bin/xray api statsquery --server=127.0.0.1:10085 >/dev/null
    # Verify the published HTTPS port with certificate and IP validation (never -k).
    curl --noproxy '*' --fail --silent --show-error --max-time 10 \
        --cacert "$INSTALL_DIR/tls/current/fullchain.pem" \
        --connect-to "$PUBLIC_IP:$ADMIN_PORT:127.0.0.1:$ADMIN_PORT" \
        "https://$PUBLIC_IP:$ADMIN_PORT/health/ready" >/dev/null
    install_renewal_timer
    show_result
}

main "$@"
