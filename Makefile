.PHONY: build test clean

build:
	@mkdir -p bin
	go build -o bin/odoo19-conformance .
	go build -o bin/minttoken ./cmd/minttoken
	go build -o bin/probetoken ./cmd/probetoken
	@echo "Build completed! Binaries located in bin/"

test:
	go test -v ./...

clean:
	rm -rf bin/
