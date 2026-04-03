# Build and install agentpack to ~/bin (override: make install INSTALL_DIR=/usr/local/bin)

CARGO ?= cargo
INSTALL_DIR ?= $(HOME)/bin
BINARY := agentpack
RELEASE_BIN := target/release/$(BINARY)

.PHONY: all build release install uninstall

all: release

build:
	$(CARGO) build

release:
	$(CARGO) build --release

install: release
	mkdir -p "$(INSTALL_DIR)"
	cp "$(RELEASE_BIN)" "$(INSTALL_DIR)/$(BINARY)"
	chmod 755 "$(INSTALL_DIR)/$(BINARY)"

uninstall:
	rm -f "$(INSTALL_DIR)/$(BINARY)"
