.PHONY: all build test vet tidy clean run

BIN := bin/agentgrid

all: build

build:
	go build -o $(BIN) ./cmd/agentgrid

test:
	go test ./...

vet:
	go vet ./...

tidy:
	go mod tidy

run: build
	./$(BIN)

clean:
	rm -rf bin/
