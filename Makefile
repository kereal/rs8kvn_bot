.PHONY: build test lint fmt clean run

BINARY := rs8kvn_bot

build:
	go build -o $(BINARY) ./cmd/bot

test:
	go test ./...

lint:
	golangci-lint run

fmt:
	gofmt -s -w .

run: build
	./$(BINARY)

clean:
	rm -f $(BINARY)
	rm -f coverage.out coverage.html
