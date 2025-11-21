DESTDIR ?=
CONFIGDIR = $(DESTDIR)/etc/xdg/elephant/providers

GO_BUILD_FLAGS = -buildvcs=false -buildmode=plugin -trimpath
PLUGIN_NAME = vscode.so

.PHONY: all build install uninstall clean

all: build

build:
	go build $(GO_BUILD_FLAGS)

install: build
	# Install plugin
	install -Dm 755 $(PLUGIN_NAME) $(CONFIGDIR)/$(PLUGIN_NAME)

uninstall:
	rm -f $(CONFIGDIR)/$(PLUGIN_NAME)

clean:
	rm -f $(PLUGIN_NAME)
