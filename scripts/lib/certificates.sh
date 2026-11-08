#!/usr/bin/env bash

CERTBOT_IMAGE=certbot/certbot:v5.9.0

certbot_run() {
    docker run --rm --log-driver none --read-only --cap-drop ALL --cap-add NET_BIND_SERVICE \
        --security-opt no-new-privileges:true --pids-limit 128 \
        --tmpfs /tmp:rw,nosuid,size=128m -p 80:80/tcp \
        -v "$INSTALL_DIR/acme:/etc/letsencrypt" \
        -v "$INSTALL_DIR/acme-logs:/var/log/letsencrypt" \
        "$CERTBOT_IMAGE" --work-dir /tmp/work "$@"
}

publish_certificate() {
    local cert=$1 key=$2 generation
    openssl x509 -in "$cert" -noout -checkend 86400 >/dev/null || fail 'Сертификат истекает менее чем через сутки.'
    openssl x509 -in "$cert" -noout -checkip "$PUBLIC_IP" >/dev/null || fail 'Сертификат не соответствует IP сервера.'
    local cert_key private_key
    cert_key=$(openssl x509 -in "$cert" -pubkey -noout | openssl pkey -pubin -outform DER | sha256sum)
    private_key=$(openssl pkey -in "$key" -pubout -outform DER | sha256sum)
    [[ $cert_key == "$private_key" ]] || fail 'Закрытый ключ не соответствует сертификату.'
    if [[ -f $INSTALL_DIR/tls/current/fullchain.pem ]] && cmp -s "$cert" "$INSTALL_DIR/tls/current/fullchain.pem"; then return; fi
    generation=$(mktemp -d "$INSTALL_DIR/tls/cert-XXXXXXXX")
    install -m 600 -o 65532 -g 65532 "$cert" "$generation/fullchain.pem"
    install -m 600 -o 65532 -g 65532 "$key" "$generation/privkey.pem"
    chown 65532:65532 "$generation"
    ln -sfn "$(basename "$generation")" "$INSTALL_DIR/tls/.current-next"
    mv -Tf "$INSTALL_DIR/tls/.current-next" "$INSTALL_DIR/tls/current"
    # Keep the previous generations: an in-flight handshake may still use them.
}

selfsigned_certificate() {
    if [[ -f $INSTALL_DIR/tls/current/fullchain.pem ]] && \
        openssl x509 -in "$INSTALL_DIR/tls/current/fullchain.pem" -noout -checkend 2592000 >/dev/null && \
        openssl x509 -in "$INSTALL_DIR/tls/current/fullchain.pem" -noout -checkip "$PUBLIC_IP" >/dev/null; then return; fi
    local temp
    temp=$(mktemp -d "$INSTALL_DIR/.certificate-XXXXXXXX")
    openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -sha256 -nodes -days 365 \
        -subj "/CN=$PUBLIC_IP" -addext "subjectAltName=IP:$PUBLIC_IP" \
        -addext 'basicConstraints=critical,CA:FALSE' -addext 'extendedKeyUsage=serverAuth' \
        -keyout "$temp/key.pem" -out "$temp/cert.pem" >/dev/null 2>&1
    publish_certificate "$temp/cert.pem" "$temp/key.pem"
    rm -rf -- "$temp"
}

obtain_certificate() {
    if [[ $TLS_MODE == selfsigned ]]; then selfsigned_certificate; return; fi
    if port_available 80 && certbot_run certonly --standalone --non-interactive --agree-tos \
        --register-unsafely-without-email --preferred-challenges http --http-01-address 0.0.0.0 \
        --required-profile shortlived --ip-address "$PUBLIC_IP" --cert-name proxyk-admin \
        --keep-until-expiring; then
        publish_certificate "$INSTALL_DIR/acme/live/proxyk-admin/fullchain.pem" "$INSTALL_DIR/acme/live/proxyk-admin/privkey.pem"
        TLS_MODE=acme
        save_settings
        return
    fi
    [[ $TLS_MODE != acme ]] || fail 'Не удалось получить ACME-сертификат. Проверьте доступность 80/TCP снаружи. Переход на HTTP запрещён.'
    if [[ ${ALLOW_SELF_SIGNED:-0} != 1 ]] && ! ask_yes 'ACME недоступен. Использовать самоподписанный HTTPS-сертификат с ручной проверкой fingerprint?'; then
        fail 'Сертификат не получен. Откройте 80/TCP либо явно задайте ALLOW_SELF_SIGNED=1.'
    fi
    TLS_MODE=selfsigned
    selfsigned_certificate
    save_settings
}

install_renewal_timer() {
    cat >/etc/systemd/system/proxyk-admin-renew.service <<EOF
[Unit]
Description=Renew ProxyK Admin HTTPS certificate
After=docker.service network-online.target
Requires=docker.service
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=$INSTALL_DIR/source/scripts/renew-certificate.sh $INSTALL_DIR
UMask=0077
TimeoutStartSec=15min
EOF
    cat >/etc/systemd/system/proxyk-admin-renew.timer <<'EOF'
[Unit]
Description=Check ProxyK Admin certificate every six hours

[Timer]
OnCalendar=*-*-* 00,06,12,18:00:00
RandomizedDelaySec=15min
Persistent=true

[Install]
WantedBy=timers.target
EOF
    systemctl daemon-reload
    systemctl enable --now proxyk-admin-renew.timer
    systemctl is-active --quiet proxyk-admin-renew.timer
}
