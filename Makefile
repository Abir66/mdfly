.PHONY: build test lint migrate-up migrate-down dev-seed

BIN_DIR := bin
DATABASE_URL ?= postgres://localhost:5432/mdfly?sslmode=disable

$(BIN_DIR):
	mkdir -p $(BIN_DIR)

build: $(BIN_DIR)
	go build -o $(BIN_DIR)/mdfly ./cmd/mdfly
	go build -o $(BIN_DIR)/mdfly-server ./cmd/mdfly-server

test:
	go test ./...

lint:
	golangci-lint run ./...

migrate-up:
	migrate -path migrations -database "$(DATABASE_URL)" up

migrate-down:
	migrate -path migrations -database "$(DATABASE_URL)" down

dev-seed:
	@echo "No migrations yet — run S01 first."
