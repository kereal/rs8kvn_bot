.PHONY: build test lint fmt clean run admin admin-dev admin-serve check

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

# admin-serve runs the panel alone on a throwaway copy of data/rs8kvn.db, with
# demo rows when the copy is empty. No bot, no node clients, no real panels.
# Ready line prints the URL and token.
admin-serve:
	go run ./tools/adminserve -seed

# check runs every gate that does not need a browser: formatting, vet, panel
# type check and the package tests. CI runs the same checks plus lint and race.
check:
	gofmt -l $$(git ls-files '*.go' | grep -v '^vendor/') | tee /tmp/gofmt-report
	@test ! -s /tmp/gofmt-report
	go vet ./...
	cd admin && npm run check
	go test ./...

run: build
	./$(BINARY)

clean:
	rm -f $(BINARY)
	rm -f coverage.out coverage.html
