# ProxyK Admin

Минималистичная VPN-панель для Linux: Go, SQLite, Vue 3 и Xray-core.
В этой версии реализуется **этап 1** из задания: инфраструктура и установка.
Вход в веб-панель, управление VPN-пользователями, VLESS-ссылки, QR-коды и учёт
трафика относятся к этапам 2–3 и пока отсутствуют. Страница показывает фактическую
готовность базы и Xray. Она не является готовой пользовательской VPN-панелью.

Статус проведённых проверок и оставшиеся ограничения: [docs/validation.md](docs/validation.md).

## Требования

- Ubuntu 22.04 / 24.04 / 26.04 / 26.10 или Debian 12 / 13 с systemd.
- Архитектура amd64 или arm64, root для установки.
- Публичный IPv4, назначенный серверу. За NAT нужны явный `PUBLIC_IP`,
  `ALLOW_NAT=1` и проброс портов.
- Docker Engine и Docker Compose Plugin с поддержкой `up --wait`.
  Если их нет, установщик автоматически ставит их из официального репозитория Docker
  и запускает службу. Подтверждение и `INSTALL_DOCKER=1` не требуются.
- Для сборки на сервере рекомендуется 2 ГБ RAM и 4 ГБ свободного диска.
  Это рекомендация для сборки, не измеренный минимум работающей панели.
- Входящий TCP: выбранный порт панели (по умолчанию 2053), VPN 443 либо 8443,
  а также **80 для ACME HTTP-01**, включая все последующие продления.
  Исходящий HTTPS нужен для загрузки образов, зависимостей и ACME.

Docker публикует порты собственными правилами; обычного UFW может быть недостаточно.
Ограничивайте доступ к панели в firewall провайдера или цепочке `DOCKER-USER`.
Установщик не меняет firewall. При установке Docker конфликтующие пакеты дистрибутива
(`docker.io`, `docker-compose`, `docker-compose-v2`, `docker-doc`, `docker-buildx`,
`podman-docker`, `containerd`, `runc`) автоматически заменяются официальными.
Очистка данных Docker и `apt purge` / `autoremove` не выполняются, но замена пакетов
может прервать работу существующих контейнеров. Готовая работающая установка Docker
с Compose используется без переустановки. Для запрета установки пакетов Docker
можно явно задать `INSTALL_DOCKER=0`.

## Установка из публичного репозитория

Репозиторий: [kkirill412/proxyk-admin-linux](https://github.com/kkirill412/proxyk-admin-linux).
На поддерживаемом Ubuntu/Debian-сервере выполните:

```bash
sudo apt-get update
sudo apt-get install -y git ca-certificates curl
git clone --depth 1 --branch main https://github.com/kkirill412/proxyk-admin-linux.git proxyk-admin
cd proxyk-admin
sudo bash scripts/install.sh
```

Клонирование по HTTPS не требует аккаунта GitHub, токена или SSH-ключа.
Команда создаёт новый каталог `proxyk-admin`; если проект уже клонирован, перейдите
в его каталог и выполните только `sudo bash scripts/install.sh`.
Go, Node.js и npm на сервер устанавливать не нужно: приложение и frontend собираются
в Docker. Рабочие данные и установленная копия проекта размещаются в `/opt/proxyk-admin`.
Ветка `main` содержит текущую разработку этапа 1; ограничения версии описаны выше.

Скрипт читает ответы из `/dev/tty`, в том числе при запуске через `curl | bash`.
Запрашивает порт панели, выбирает VPN-порт, создаёт случайного администратора,
сохраняет секреты, собирает Go-бинарник со встроенной сборкой Vue и запускает сервисы.
HTTPS проверяется с валидацией сертификата и IP, затем включается продление.

Для неинтерактивной установки с публичным сертификатом:

```bash
sudo env ADMIN_PORT=2053 NONINTERACTIVE=1 TLS_MODE=acme bash scripts/install.sh
```

Для явно выбранного резервного HTTPS:

```bash
sudo env ADMIN_PORT=2053 NONINTERACTIVE=1 TLS_MODE=selfsigned bash scripts/install.sh
```

В режиме `TLS_MODE=auto` (по умолчанию) при неудаче ACME установщик запрашивает
согласие на самоподписанный сертификат. Без терминала требуется
`ALLOW_SELF_SIGNED=1`; иначе установка завершается ошибкой. На HTTP перехода нет.

`PUBLIC_IP` можно задать явно; адрес обязательно проверяется. Порт 80 зарезервирован
для ACME. Занятый порт панели отклоняется. Если VPN 443 занят или выбран для панели,
используется 8443; если и он занят, установка останавливается.

### Однокомандная установка через HTTPS

Установка версии 0.1.2 из [GitHub Release](https://github.com/kkirill412/proxyk-admin-linux/releases/tag/v0.1.2):

```bash
curl -fsSL https://github.com/kkirill412/proxyk-admin-linux/releases/download/v0.1.2/install.sh | sudo bash
```

Docker Engine, Compose Plugin и недостающие системные зависимости устанавливаются
автоматически. Скрипт запрашивает порт панели; для установки без ввода:

```bash
curl -fsSL https://github.com/kkirill412/proxyk-admin-linux/releases/download/v0.1.2/install.sh | sudo env ADMIN_PORT=2053 NONINTERACTIVE=1 TLS_MODE=acme bash
```

Прямая передача `scripts/install.sh` с `raw.githubusercontent.com` в Bash без исходников
не сработает: скрипту нужны файлы проекта либо `SOURCE_URL` и `SOURCE_SHA256`.

Для подготовки файлов релиза выполните из клона проекта:

```bash
make release RELEASE_BASE_URL=https://github.com/kkirill412/proxyk-admin-linux/releases/download/v0.1.2
```

Три файла `bin/release/install.sh`, `bin/release/proxyk-admin-0.1.2.tar.gz` и
`bin/release/SHA256SUMS` хранятся в Git и прикладываются к GitHub Release соответствующей
версии. Остальные файлы `bin` игнорируются. При выпуске новой версии обновите `VERSION`
и адрес `RELEASE_BASE_URL`; опубликованные архивы уже выпущенных версий не заменяйте.
После отправки тега `v<VERSION>` workflow `Publish release` проверяет файлы,
повторяемость сборки архива и загружает их в GitHub Release. Уже загруженные файлы
сверяются с локальными; при несовпадении публикация останавливается без перезаписи.
Пока реализован только этап 1, новые релизы автоматически помечаются `prerelease`.
В выпущенный установщик встроены URL архива и его SHA-256; до распаковки проверяется
хеш, пути архива и отсутствие ссылок. Распаковка совместима с системным Python 3.10/3.11
без поддержки `tarfile` extraction filters; владельцы и специальные права из архива
не восстанавливаются. URL не должен указывать на изменяемый архив.
HTTPS защищает доставку самого установщика; SHA-256 не заменяет доверие к издателю.
Скрипт из репозитория также принимает `SOURCE_URL` и `SOURCE_SHA256`, если запускается
через stdin без встроенных данных релиза.

## Первоначальный доступ и пароль

После успешных проверок выводятся адрес `https://IP:PORT`, VPN-порт и статусы сервисов.
В интерактивном режиме случайный логин и пароль выводятся **только в `/dev/tty`**,
а временный файл удаляется. Пароль содержит 32 символа, генерируется `crypto/rand`,
в SQLite хранится bcrypt-хеш с cost 12. Логин также случайный.

В неинтерактивном режиме данные остаются в `/opt/proxyk-admin/initial-credentials.json`
с правами `0600`, владелец root. Прочитайте по защищённому SSH, сохраните в менеджере
паролей и удалите этот файл. Не отправляйте его в общие журналы CI.
При прерывании установки данные могут оставаться в `data/initial-credentials.json`;
повторный запуск завершит установку и безопасно передаст их.

**На этапе 1 формы входа ещё нет.** Данные администратора подготовлены для этапа 3;
текущая страница содержит только общую проверку сервисов, без секретов и действий.
Создание пользователей и получение подключения станут доступны на этапах 2–3.

Для самоподписанного сертификата перед вводом любых данных сравните SHA-256 fingerprint
в просмотрщике сертификата браузера с результатом команды на сервере по доверенному SSH:

```bash
sudo openssl x509 -in /opt/proxyk-admin/tls/current/fullchain.pem -noout -fingerprint -sha256
```

Предупреждение браузера ожидаемо; пропускать его без сравнения fingerprint нельзя.

### Сброс пароля

```bash
cd /opt/proxyk-admin
sudo docker compose exec -T app /usr/local/bin/proxyk-admin reset-admin-password
sudo cat data/initial-credentials.json
sudo rm data/initial-credentials.json
```

Команда не выводит пароль в Docker logs. Она сохраняет логин, меняет хеш и увеличивает
версию учётных данных для будущей инвалидизации сессий. После сброса удалите также
старый `/opt/proxyk-admin/initial-credentials.json`, если он сохранился от установки.

## HTTPS и продление

Два постоянно работающих контейнера: Go-приложение и официальный
`ghcr.io/xtls/xray-core:26.10.31`. Certbot `certbot/certbot:v5.9.0` — кратковременный
контейнер, запускаемый установщиком и systemd. Он нужен для ACME; дополнительного
постоянно работающего reverse proxy нет. Go сам завершает TLS.

Certbot использует IP SAN, обязательный ACME-профиль `shortlived` и HTTP-01 на 80/TCP.
Сертификаты Let's Encrypt для IP действуют около шести дней. Timer проверяет продление
каждые шесть часов со случайной задержкой до 15 минут и догоняет пропущенный запуск
после перезагрузки. Ключ и цепочка публикуются вместе через атомарную смену символьной
ссылки; Go загружает актуальную пару при новом TLS handshake без перезапуска.
При ошибке продления сервис завершится с ошибкой; истёкший сертификат не маскируется
статусом healthy. Настройте мониторинг `/health/ready` и systemd-ошибок.

Самоподписанный сертификат действует год, timer заменяет его при остатке менее 30 дней.
**При замене fingerprint меняется**, его нужно снова проверить по SSH.
После успешного ACME резервный сертификат автоматически не подставляется.

```bash
sudo systemctl list-timers proxyk-admin-renew.timer
sudo systemctl start proxyk-admin-renew.service
sudo journalctl -u proxyk-admin-renew.service --since today
```

## Данные, перезапуск и повторная установка

```text
/opt/proxyk-admin/
  compose.yaml               конфигурация запуска
  .env                       IP, порты, режим сертификата
  source/                    установленный исходный код
  data/app.db                единственная база SQLite
  data/app.db-wal, -shm      служебные файлы WAL при работе
  xray/config.json           конфигурация Xray с постоянными ключами
  tls/current/               текущий сертификат и ключ
  acme/                      ACME-аккаунт и выпущенные сертификаты
  acme-logs/                 журнал Certbot
```

SQLite: WAL, `busy_timeout=10000`, foreign keys, `synchronous=FULL`, один connection
для сериализации записи, транзакции `BEGIN IMMEDIATE` для координации CLI и сервера.
Миграции встроены в бинарник, версионированы, применяются транзакционно; изменение
уже применённого SQL и открытие схемы более новой версии отклоняются.

Все данные — bind mounts. Контейнеры работают от UID/GID 65532, без capabilities,
с read-only root filesystem и `no-new-privileges`. API Xray 10085 не опубликован
на хосте. Управляющее соединение идёт через внутреннюю сеть; для публикации HTTPS
панель также имеет отдельную сеть `panel`, Xray — сеть `egress` для VPN-порта
и исходящих соединений. Docker socket внутрь контейнеров не передаётся.

Повторная установка той же версии сохраняет администратора, ключи, IP и порты.
Случайные значения создаются только при первой инициализации. Обычный запуск сервера
не перезаписывает Xray-конфигурацию. `unless-stopped` восстанавливает сервисы после
перезагрузки Linux, если Docker включён. Остановка через `docker compose stop`
считается явной остановкой и требует последующего `up -d`.
Если Xray-конфигурация сохранилась, а база потеряна, инициализация останавливается
с требованием восстановить базу; новые несовместимые ключи молча не создаются.

## Обновление и резервное копирование

Автоматическое обновление с rollback и команды online backup/restore относятся
к **этапу 4 и ещё не реализованы**. Установщик отказывается менять установленную
версию. Не используйте повторную установку как способ обновления и не копируйте
один открытый `app.db`, игнорируя WAL.

До этапа 4 резервную копию можно сделать при полностью остановленных сервисах и
timer: сохранить каталог установки целиком с владельцами и правами, включая `data`,
`xray`, `tls`, `acme` и `.env`; затем вернуть исходное состояние сервисов. Храните
копию как секрет и проверьте восстановление на отдельной машине. Это эксплуатационный
обходной путь, не реализация требуемого online backup API.

## Разработка и тестирование

Для локальной сборки: Go 1.27.3, Node.js 26.13.0 (или совместимый Node >=22.12), npm,
OpenSSL. Все прямые зависимости зафиксированы; npm lockfile и Go checksums включены.
TypeScript 5.9.3 выбран как совместимая версия для `vue-tsc`; компиляция проверяется.

```bash
make build           # Vue + TypeScript + Go; результат bin/proxyk-admin
make test            # Go race tests, vet, проверки установщика
make test-native     # настоящий Xray: HTTPS, gRPC, отказ и перезапуск
sudo bash scripts/test-compose.sh
```

`make test-native` собирает официальный Xray из точно закреплённого commit релиза
26.10.31. Можно передать `XRAY_BINARY=/абсолютный/путь/xray` для готового бинарника.
Этот тест проверяет инфраструктуру; передача пользовательского VPN-трафика будет
проверяться после реализации этапа 2. Интеграционный тест в обычном `go test` пропускается,
если реальные бинарники не заданы — mock не выдаётся за реальную интеграцию.

## Архитектура

HTTP controller → usecase → service / repository / Xray adapter.
`cmd/server` связывает зависимости и реализует CLI. `internal/migration` хранит SQL,
`web` содержит Vue и `embed`, `deploy` — образы и Compose, `scripts` — установка
и проверки. Разделение на слои применяется к реализованным сценариям; пустые
репозитории и интерфейсы будущего управления пользователями не добавлены.

## Диагностика

```bash
cd /opt/proxyk-admin
sudo docker compose ps
sudo docker compose logs --tail 100 app xray
sudo docker compose exec -T app /usr/local/bin/proxyk-admin healthcheck
sudo docker compose exec -T xray /usr/local/bin/xray api statsquery --server=127.0.0.1:10085
sudo docker compose run --rm --no-deps xray run -test -config /etc/proxyk-admin/xray/config.json
sudo systemctl status docker proxyk-admin-renew.timer
```

`/health/live` проверяет HTTP-процесс; `/health/ready` возвращает 200 только при
доступной и инициализированной SQLite и успешном `StatsService.GetSysStats` Xray.
При недоступном Xray возвращается 503. Локальная успешная проверка HTTPS не доказывает,
что firewall провайдера пропускает панель; после установки проверьте URL со своего
компьютера. Секретные файлы и полный Xray JSON не прикладывайте к диагностике.

## Официальные источники

- [API Xray и StatsService](https://xtls.github.io/en/config/api.html).
- [Релиз Xray 26.10.31](https://github.com/XTLS/Xray-core/releases/tag/v26.10.31).
- [IP-сертификаты Let's Encrypt и Certbot](https://letsencrypt.org/2026/03/11/shorter-certs-certbot).
- [Certbot: standalone и автоматическое продление](https://eff-certbot.readthedocs.io/en/stable/using.html).
- [Установка Docker на Ubuntu](https://docs.docker.com/engine/install/ubuntu/).
- [SQLite PRAGMA](https://www.sqlite.org/pragma.html).

3x-ui использован только как функциональный ориентир. Его исходный код не копировался.
