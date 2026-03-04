.PHONY: build test test-integration lint run docker docker-up docker-down clean

APP_NAME := fx-quotes
BUILD_DIR := dist

build:
	CGO_ENABLED=0 go build -o $(BUILD_DIR)/$(APP_NAME) ./cmd/server

run:
	set -a && . ./.env && set +a && go run ./cmd/server

test:
	go test ./...

test-integration:
	go test -tags integration -v -count=1 ./internal/repository/

lint:
	golangci-lint run ./...

docker:
	docker-compose build

docker-up:
	docker-compose up --build -d

docker-down:
	docker-compose down

clean:
	rm -rf $(BUILD_DIR)
