.PHONY: sqlc run build tidy lint test migrate-down

# Pin sqlc to the version the checked-in code was generated with, so `make sqlc`
# is reproducible and CI doesn't drift onto a newer codegen.
SQLC_VERSION := v1.31.1

sqlc:
	go run github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION) generate

tidy:
	go mod tidy

build:
	go build -o bin/server ./cmd/server

run:
	go run ./cmd/server

lint:
	golangci-lint run ./...

test:
	go test ./...

# Roll back all migrations (requires DATABASE_URL). Useful in dev.
migrate-down:
	go run github.com/golang-migrate/migrate/v4/cmd/migrate@latest \
		-path internal/db/migrations \
		-database "$(DATABASE_URL)" down
