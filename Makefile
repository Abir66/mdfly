.PHONY: build test test-deploy test-release lint release migrate-up migrate-down dev-seed

BIN_DIR := bin
DEV_DB_URL := postgres://mdfly:secret@localhost:5432/mdfly?sslmode=disable
DATABASE_URL ?= $(DEV_DB_URL)
# Single source of truth for the version. `make release` turns this into the git
# tag; goreleaser then reads the tag. The -dev suffix keeps a local build from
# claiming to be the released artifact of the same number.
VERSION := $(shell tr -d '[:space:]' < VERSION)

$(BIN_DIR):
	mkdir -p $(BIN_DIR)

build: $(BIN_DIR)
	go build -ldflags "-X main.version=$(VERSION)-dev" -o $(BIN_DIR)/mdfly ./cmd/mdfly
	go build -o $(BIN_DIR)/mdfly-server ./cmd/mdfly-server

# Tags VERSION and pushes, which is what starts the release workflow.
release:
	./scripts/release.sh

test:
	go test ./...

test-deploy:
	./deploy/deploy_test.sh

test-release:
	./scripts/release_test.sh

lint:
	golangci-lint run ./...

migrate-up:
	migrate -path migrations -database "$(DATABASE_URL)" up

migrate-down:
	migrate -path migrations -database "$(DATABASE_URL)" down

dev-seed:
	@docker inspect mdfly-dev-postgres >/dev/null 2>&1 || \
		docker run -d --name mdfly-dev-postgres \
			-e POSTGRES_DB=mdfly \
			-e POSTGRES_USER=mdfly \
			-e POSTGRES_PASSWORD=secret \
			-p 5432:5432 \
			postgres:16-alpine
	@echo "Waiting for Postgres..."
	@until docker exec mdfly-dev-postgres pg_isready -U mdfly -d mdfly -q; do sleep 0.5; done
	@DATABASE_URL=$(DEV_DB_URL) $(MAKE) migrate-up
