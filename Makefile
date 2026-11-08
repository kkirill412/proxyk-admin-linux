.PHONY: build frontend test test-native check-shell release

frontend:
	cd web && npm ci --no-audit --no-fund && npm run build

build: frontend
	go build -trimpath -o bin/proxyk-admin ./cmd/server

test: frontend
	go test -race ./...
	go vet ./...
	bash scripts/test-installer.sh

test-native: build
	bash scripts/test-native.sh

check-shell:
	shellcheck -x -P SCRIPTDIR scripts/*.sh scripts/lib/*.sh

release:
	bash scripts/pack-release.sh "$(RELEASE_BASE_URL)"
