.PHONY: build test lint hooks clean

# The hooks live in .git/config, not the tree, so they are the one part of
# setup that can silently not be there. Wired into build and test because those
# are the first things anyone runs. It only acts when this directory is the
# repository root, so a tarball unpacked inside an unrelated checkout never
# repoints that repository's hooks.
hooks:
	@test "$$(git rev-parse --show-toplevel 2>/dev/null)" = "$$(pwd -P)" || exit 0; \
	 test "$$(git config --get core.hooksPath)" = .githooks || { \
	   git config core.hooksPath .githooks && \
	   echo "hooks: core.hooksPath -> .githooks"; \
	 }

build: hooks
	go build ./...

# Clear the test cache so results are never stale.
test: hooks
	go clean -testcache
	go test ./...

lint:
	golangci-lint run ./...

clean:
	go clean -cache -testcache
	rm -rf bin
