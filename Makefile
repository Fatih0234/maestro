BINARY := maestro
CMD := ./cmd/maestro
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X main.version=$(VERSION)"

.PHONY: build install clean test

build:
	go build $(LDFLAGS) -o $(BINARY) $(CMD)

install: build
	mkdir -p $(HOME)/bin
	cp $(BINARY) $(HOME)/bin/$(BINARY)
	@echo "Installed to $(HOME)/bin/$(BINARY)"
	@echo "Add to your shell profile if needed:"
	@echo '  alias maestro="$(HOME)/bin/$(BINARY)"'

clean:
	rm -f $(BINARY)

test:
	go test ./...
