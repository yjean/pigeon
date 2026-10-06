PREFIX ?= $(HOME)/.local
BINDIR ?= $(PREFIX)/bin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build install uninstall test clean

build: ## Build ./pigeon
	go build -ldflags "$(LDFLAGS)" -o pigeon .

install: test ## Test, then install to $(BINDIR) (default ~/.local/bin)
	@mkdir -p "$(BINDIR)"
	go build -ldflags "$(LDFLAGS)" -o "$(BINDIR)/pigeon" .
	@echo "✓ pigeon $(VERSION) installed to $(BINDIR)/pigeon"

uninstall: ## Remove the installed binary (config, tokens and cache are kept)
	rm -f "$(BINDIR)/pigeon"

test: ## Vet and run the tests
	go vet ./...
	go test ./...

clean:
	rm -f pigeon
