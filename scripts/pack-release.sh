#!/usr/bin/env bash
set -Eeuo pipefail
ROOT=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
BASE_URL=${1:?Укажите HTTPS-адрес каталога публикации релиза}
[[ $BASE_URL =~ ^https://[a-zA-Z0-9._:/-]+$ ]] || { echo 'Нужен HTTPS URL без query-параметров.' >&2; exit 1; }
VERSION=$(cat "$ROOT/VERSION")
OUT=$ROOT/bin/release
mkdir -p "$OUT"
ARCHIVE=proxyk-admin-$VERSION.tar.gz
tar -C "$ROOT" --sort=name --mtime='UTC 2026-01-01' --owner=0 --group=0 --numeric-owner \
    --exclude='web/node_modules' --exclude='web/dist' \
    -czf "$OUT/$ARCHIVE" VERSION README.md Makefile go.mod go.sum .dockerignore .gitignore cmd internal web deploy scripts docs tests
SHA=$(sha256sum "$OUT/$ARCHIVE" | cut -d ' ' -f 1)
python3 - "$ROOT/scripts/install.sh" "$OUT/install.sh" "${BASE_URL%/}/$ARCHIVE" "$SHA" <<'PY'
from pathlib import Path
import sys
source, target, url, sha = sys.argv[1:]
text = Path(source).read_text().replace("RELEASE_SOURCE_URL=''", f"RELEASE_SOURCE_URL='{url}'")
text = text.replace("RELEASE_SOURCE_SHA256=''", f"RELEASE_SOURCE_SHA256='{sha}'")
Path(target).write_text(text)
PY
chmod 755 "$OUT/install.sh"
(cd "$OUT" && sha256sum "$ARCHIVE" install.sh >SHA256SUMS)
printf 'Файлы подготовлены в %s. После публикации этих файлов команда установки:\n' "$OUT"
printf 'curl -fsSL %s/install.sh | sudo bash\n' "${BASE_URL%/}"
