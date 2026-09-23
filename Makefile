.PHONY: all build test clean package docker-build docker-up docker-down

all: build

build:
	@./build.sh

test:
	go test ./...

package: build

clean:
	rm -rf target

docker-build:
	docker build -t jobcon:latest .

docker-up:
	docker compose up -d

docker-down:
	docker compose down
