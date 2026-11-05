.PHONY: build test lint fmt clean run admin admin-dev

BINARY := rs8kvn_bot

build:
	go build -o $(BINARY) ./cmd/bot

test:
	go test ./...

lint:
	golangci-lint run

fmt:
	gofmt -s -w $$(find . -type f -name '*.go' -not -path './.git/*')

# admin rebuilds the embedded panel. dist/ is committed, so plain `go build`
# works without Node; run this only after changing files under admin/src.
admin:
	cd admin && npm run build

# admin-dev runs Vite with HMR; it proxies /admin/api to ADMIN_LISTEN.
admin-dev:
	cd admin && npm run dev

run: build
	./$(BINARY)

clean:
	rm -f $(BINARY)
	rm -f coverage.out coverage.html
