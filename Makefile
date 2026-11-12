-include .env
export

.PHONY: run test test-integration lint migrate-up migrate-down sqlc db-up db-down

run:
	go run ./cmd/api

test:
	go test -race ./...

test-integration:
	go test -race -tags=integration ./test/integration/...

lint:
	golangci-lint run

# goose reads APP_USER_PASSWORD from the environment (ENVSUB) when creating app_user.
migrate-up:
	goose -dir db/migrations postgres "$(MIGRATION_DATABASE_URL)" up

# Rolls back one migration.
migrate-down:
	goose -dir db/migrations postgres "$(MIGRATION_DATABASE_URL)" down

# sqlc needs cgo to build, so it runs from its Docker image (works on Windows).
# MSYS_NO_PATHCONV stops Git Bash from rewriting /src into a Windows path.
sqlc:
	MSYS_NO_PATHCONV=1 docker run --rm -v "$(CURDIR):/src" -w /src sqlc/sqlc:1.30.0 generate

db-up:
	docker compose up -d --wait postgres

db-down:
	docker compose down
