# Build and test DeckChat. `make release` produces dist/deckchat.tar.gz, the
# archive the player copies to their Deck and installs with install.sh.

WEB := companion/internal/server/web
# macOS's bsdtar would otherwise add AppleDouble files; GNU tar (CI) sets owners.
TAR_FLAGS := $(shell tar --version 2>/dev/null | grep -q bsdtar && echo --no-xattrs --no-mac-metadata || echo --owner=0 --group=0)

.PHONY: all phone deckchat test test-linux lint-addon release clean

all: release

phone:
	cd phone && npm ci && npm run build && npm run check:local
	find $(WEB) -mindepth 1 ! -name .keep -delete
	cp -R phone/dist/. $(WEB)/

deckchat: phone
	cd companion && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o ../dist/deckchat/deckchat ./cmd/deckchat

test:
	cd companion && go vet ./... && go test -race ./...
	cd phone && npm test && npm run typecheck

test-linux:
	test/linux/run.sh

lint-addon:
	docker run --rm -v "$(CURDIR)/addon:/data" -w /data pipelinecomponents/luacheck luacheck .

release: deckchat
	rm -rf dist/deckchat/addon && mkdir -p dist/deckchat/addon && cp -R addon/DeckChat dist/deckchat/addon/DeckChat
	rm -f dist/deckchat/addon/DeckChat/Pairing.lua
	cp install/install.sh install/uninstall.sh install/deckchat.service install/70-deckchat-uinput.rules dist/deckchat/
	cp README.md LICENSE dist/deckchat/
	scripts/third-party-licenses.sh > dist/deckchat/THIRD_PARTY_LICENSES.txt
	COPYFILE_DISABLE=1 tar $(TAR_FLAGS) -C dist -czf dist/deckchat.tar.gz deckchat
	cd dist && shasum -a 256 deckchat.tar.gz > deckchat.tar.gz.sha256
	@echo "built dist/deckchat.tar.gz"

clean:
	rm -rf dist phone/dist
	find $(WEB) -mindepth 1 ! -name .keep -delete
