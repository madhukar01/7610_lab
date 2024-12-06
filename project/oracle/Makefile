.PHONY: build test test-integration test-unit clean

build:
	go build -o bin/oracle-node ./cmd/node

test: test-unit test-integration

test-unit:
	go test -v ./tests/unit/...

test-integration:
	go test -v ./tests/integration/...

clean:
	rm -rf bin/
