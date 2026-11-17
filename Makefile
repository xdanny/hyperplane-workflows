# Hyperplane developer + quality gates. The same gates run in CI and gate our
# own code exactly like a workflow gates generated code.
.PHONY: help setup up db-up down logs build vet test test-unit test-db lint tidy gate gate-ux gate-ux-e2e seed web-install web-build web-unit ci

help:
	@echo "make setup       - build native API + web and start Postgres"
	@echo "make up          - run API + web on http://127.0.0.1:4252"
	@echo "make down        - docker compose down"
	@echo "make build       - go build ./..."
	@echo "make vet         - go vet ./..."
	@echo "make test        - full tests against disposable test databases"
	@echo "make lint        - gofmt check + go vet"
	@echo "make test-db     - start the separate disposable test server"
	@echo "make gate        - full quality gate (fmt, vet, build, test)"
	@echo "make seed        - import the in-repo knowledge defs (skills, context)"
	@echo "make web-build   - typecheck + build the React app"
	@echo "make web-unit    - unit-test the web app's pure modules (no browser)"

COMPOSE = docker compose -f infra/docker-compose.yml

# The same native runtime on Linux and macOS; Docker owns only Postgres.
setup: db-up sdk-install
	cd apps/web && npm ci
	$(MAKE) web-build
	mkdir -p bin
	go build -o bin/api ./apps/api
	go build -o bin/hyperplane ./apps/cli

up:
	./scripts/run-local.sh

db-up:
	$(COMPOSE) up -d --wait db

down:
	$(COMPOSE) down

logs:
	$(COMPOSE) logs -f db

build:
	go build ./...

vet:
	go vet ./...

# Unit tests never discover or connect to the application database.
test-unit:
	TEST_DATABASE_URL= DATABASE_URL= go test ./...

TEST_DATABASE_URL ?= postgres://hyperplane_test:hyperplane_test@127.0.0.1:15497/hyperplane_test_admin?sslmode=disable

test-db:
	docker compose -f infra/test-compose.yml up -d --wait

test:
	TEST_DATABASE_URL="$(TEST_DATABASE_URL)" DATABASE_URL= go test -count=1 ./...

tidy:
	go mod tidy

# Quality gate for our own code. Fails on any unformatted file.
lint:
	@unformatted=$$(gofmt -l . ); \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt needed:"; echo "$$unformatted"; exit 1; \
	fi
	go vet ./...

gate: sdk-test lint build test gate-ux
	@echo "QUALITY GATE PASSED"

# Internal UX quality gate — our own UI is held to the design-system + a11y
# discipline (deterministic). The codex VISUAL panel + Playwright flow scenarios
# (apps/web/e2e) are the dynamic half, run via `make gate-ux-e2e`.
gate-ux:
	@node scripts/ux-lint.mjs

# Builds the web app and owns an isolated preview; no running API is required.
gate-ux-e2e:
	cd apps/web && npm run e2e

# Knowledge defs imported by `make seed`. Defaults to the in-repo starter set so a
# fresh clone seeds a working knowledge base with no external checkout. Override
# to import a larger private KB: make seed DEFS_DIR=/path/to/custom/defs
DEFS_DIR ?= knowledge/defs

seed:
	DATABASE_URL=$${DATABASE_URL:-postgres://hyperplane:hyperplane@127.0.0.1:5432/hyperplane?sslmode=disable} \
		go run ./apps/cli seed --from $(DEFS_DIR)

web-install:
	cd apps/web && npm install

web-build:
	cd apps/web && npm run build

# The web app's pure modules (row state behind the recipe form and the project
# declarations editor), run by the Playwright runner with no browser and no
# server. Not part of `make gate`, which must pass with no node_modules.
web-unit:
	cd apps/web && npm run test:unit

ci: gate

# Official SDK runtime; the lockfile includes its pinned Codex executable.
.PHONY: sdk-install sdk-test
sdk-install:
	cd packages/agents && npm ci --ignore-scripts

sdk-test:
	cd packages/agents && npm test
