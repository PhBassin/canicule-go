BINARY := meteo_vigilance
SRC_DIR := go
DIST_DIR := dist
LDFLAGS := -s -w

.PHONY: all linux windows clean

all: linux windows

linux:
	cd $(SRC_DIR) && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o ../$(DIST_DIR)/$(BINARY)_linux_amd64 .
	@echo "→ dist/$(BINARY)_linux_amd64"

windows:
	cd $(SRC_DIR) && CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o ../$(DIST_DIR)/$(BINARY)_windows_amd64.exe .
	@echo "→ dist/$(BINARY)_windows_amd64.exe"

clean:
	rm -rf $(DIST_DIR)

vet:
	cd $(SRC_DIR) && go vet ./...
