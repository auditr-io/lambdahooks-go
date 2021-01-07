.PHONY: build test clean

build:
	export GO111MODULE=on
	go build -ldflags="-s -w" -o bin/lambdahooks .

test:
	go test -v ./...

clean:
	rm -rf ./bin ./vendor Gopkg.lock
