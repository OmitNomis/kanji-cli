# cli-kanji
#
# The review binary is tiny and cross-compiles to anything, because there is no
# cgo: the SQLite driver is pure Go. The dictionary is the expensive artefact
# and is built once here, then copied to the machine you review on.

GO      ?= go
BIN     := bin
DIST    := dist
RAW     := data/raw
DICT    := data/dict.db

# Review tier: 2 is the ~18k compounds dictionaries call common, 1 adds the
# second tier, 0 makes every compound in JMdict reviewable.
TIER    ?= 1

# No cgo, no symbol table, no local paths baked into the binary.
LDFLAGS := -s -w
BUILD   := CGO_ENABLED=0 $(GO) build -trimpath -ldflags="$(LDFLAGS)"

# Early Atoms are 32-bit only, so 386 is a first-class target. SSE2 is safe:
# every Atom has it.
PLATFORMS := linux/386 linux/amd64 linux/arm64 windows/amd64 darwin/arm64

.PHONY: all build dict fetch test race vet fmt check dist clean distclean help

all: build

## build: compile every command into bin/
build:
	@mkdir -p $(BIN)
	$(BUILD) -o $(BIN)/ ./cmd/kanji ./cmd/kanjidata ./cmd/kanji-bot

## fetch: download KANJIDIC2 and JMdict without building the dictionary
fetch: build
	$(BIN)/kanjidata -fetch-only -raw $(RAW)

## dict: build the dictionary, downloading the sources if they are missing
dict: build
	$(BIN)/kanjidata -raw $(RAW) -out $(DICT) -tier $(TIER)

## test: run every test
test:
	$(GO) test ./...

## race: run the tests under the race detector (needs a C toolchain)
race:
	CGO_ENABLED=1 $(GO) test -race ./...

## vet: run go vet
vet:
	$(GO) vet ./...

## fmt: rewrite every file with gofmt
fmt:
	gofmt -w $$(find . -name '*.go' -not -path './$(DIST)/*')

## check: gofmt diff, vet and tests, as CI would
check:
	@unformatted=$$(gofmt -l $$(find . -name '*.go' -not -path './$(DIST)/*')); \
	if [ -n "$$unformatted" ]; then echo "not gofmt-clean:"; echo "$$unformatted"; exit 1; fi
	$(GO) vet ./...
	$(GO) test ./...

## dist: cross-compile the review binary for every supported platform
dist:
	@mkdir -p $(DIST)
	@for platform in $(PLATFORMS); do \
		goos=$${platform%/*}; goarch=$${platform#*/}; \
		ext=''; [ "$$goos" = windows ] && ext='.exe'; \
		echo "  $$goos/$$goarch"; \
		GOOS=$$goos GOARCH=$$goarch GO386=sse2 $(BUILD) \
			-o $(DIST)/kanji-$$goos-$$goarch$$ext ./cmd/kanji || exit 1; \
	done
	@ls -lh $(DIST)

## clean: remove build output
clean:
	rm -rf $(BIN) $(DIST)

## distclean: also remove the downloaded sources and the built dictionary
distclean: clean
	rm -rf $(RAW) $(DICT)

## help: list these targets
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed -e 's/## /  /'
