#!/usr/bin/env bash

# This transition is intentionally limited to the installer layout of 0.2.0.
upgrade_release() (
    local backup restore=0
    local from_version=0.2.0 to_version=0.3.0
    [[ -f $INSTALL_DIR/source/VERSION && -f $INSTALL_DIR/data/app.db && -f $INSTALL_DIR/.env ]] || \
        fail 'Не найдена установка ProxyK Admin с базой данных.'
    [[ $(<"$INSTALL_DIR/source/VERSION") == "$from_version" && $(<"$SOURCE_DIR/VERSION") == "$to_version" ]] || \
        fail "Этот режим обновляет только v$from_version до v$to_version."
    cmp -s "$INSTALL_DIR/compose.yaml" "$INSTALL_DIR/source/deploy/compose.yaml" || \
        fail 'Файл compose.yaml изменён. Требуется отдельный план обновления.'
    load_settings
    docker info >/dev/null || fail 'Запустите Docker перед обновлением.'
    compose exec -T xray /usr/local/bin/xray api statsquery --server=127.0.0.1:10085 >/dev/null || \
        fail 'Запустите Xray перед обновлением.'

    install -d -m 700 "$INSTALL_DIR/backups"
    backup=$(mktemp -d "$INSTALL_DIR/backups/from-$from_version-XXXXXXXX")
    mkdir -m 700 "$backup/next-source"
    copy_source "$SOURCE_DIR" "$backup/next-source"
    unset SOURCE_DIR
    install -m 600 "$backup/next-source/deploy/compose.yaml" "$backup/next-compose.yaml"
    info "Сборка образа v$to_version. Панель продолжает работать."
    SOURCE_DIR="$backup/next-source" docker compose --project-directory "$INSTALL_DIR" \
        --env-file "$INSTALL_DIR/.env" -f "$backup/next-compose.yaml" build app

    info "Резервная копия предыдущей версии: $backup"
    trap 'finish_upgrade "$?" "$backup" "$restore"' EXIT
    trap 'exit 130' INT
    trap 'exit 143' TERM
    compose stop app
    cp -a -- "$INSTALL_DIR/data" "$backup/data"
    cp -a -- "$INSTALL_DIR/compose.yaml" "$backup/compose.yaml"
    restore=1
    mv -T -- "$INSTALL_DIR/source" "$backup/source"
    mv -T -- "$backup/next-source" "$INSTALL_DIR/source"
    mv -T -- "$backup/next-compose.yaml" "$INSTALL_DIR/compose.yaml"
    start_upgraded_app
    printf 'Обновлено до v%s. Резервная копия: %s\n' "$to_version" "$backup"
)

start_upgraded_app() {
    compose up -d --no-build --no-deps --wait --wait-timeout 180 app &&
        compose exec -T app /usr/local/bin/proxyk-admin healthcheck
}

finish_upgrade() {
    local status=$1 backup=$2 restore=$3
    if (( status == 0 )); then return; fi
    info 'Обновление прервалось. Восстанавливается предыдущая версия.'
    if (( restore )) && ! restore_previous_release "$backup"; then
        info "Не удалось восстановить файлы. Резервная копия: $backup"
        return
    fi
    if start_upgraded_app; then
        info 'Предыдущая версия восстановлена.'
    else
        info "Проверьте запуск предыдущей версии. Резервная копия: $backup"
    fi
}

restore_previous_release() {
    local backup=$1
    compose stop app || return
    if [[ -d $backup/source ]]; then
        if [[ -d $INSTALL_DIR/source ]]; then
            mv -T -- "$INSTALL_DIR/source" "$backup/failed-source" || return
        fi
        cp -a -- "$backup/source" "$INSTALL_DIR/source" || return
    fi
    mv -T -- "$INSTALL_DIR/data" "$backup/failed-data" &&
        cp -a -- "$backup/data" "$INSTALL_DIR/data" &&
        cp -a -- "$backup/compose.yaml" "$backup/restore-compose.yaml" &&
        mv -T -- "$backup/restore-compose.yaml" "$INSTALL_DIR/compose.yaml"
}
