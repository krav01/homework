.PHONY: build test test-race lint fmt images deploy e2e clean

build:
	mkdir -p bin
	go build -o bin/controller ./cmd/controller
	go build -o bin/sundayapp ./cmd/sundayapp

test:
	go test ./...

test-race:
	go test -race ./...

lint:
	golangci-lint run ./...

fmt:
	gofmt -s -w $$(find . -name '*.go' -not -path './work/*')

images:
	docker build -f Dockerfile.controller -t sunday-controller:dev .
	docker build -f Dockerfile.sundayapp -t sunday-app:dev .

deploy:
	./requirements.sh

e2e:
	./e2e/e2e.sh

clean:
	rm -rf bin coverage.out work
