.PHONY: build test clean install

build:
	@mkdir -p bin
	CGO_ENABLED=0 go build -trimpath -o bin/odoo-cli ./cmd/odoo-cli
	@echo "Built: bin/odoo-cli"

# The command line, then the Odoo core (its own module under third_party/).
test:
	go test -timeout 30m ./...
	cd third_party/packages/odoo && go test -timeout 30m ./...

install:
	./scripts/install.sh

clean:
	rm -rf bin/ dist/
