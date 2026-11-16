.PHONY: up down logs infra test run-auth run-notifications

# Start everything (infra + services) in Docker
up:
	docker compose up --build -d

down:
	docker compose down

logs:
	docker compose logs -f

# Start only the infrastructure, for running services on the host
infra:
	docker compose up -d postgres redis mongo rabbitmq

# Unit tests for every Go service
test:
	cd services/auth && go test ./...
	cd services/orders && go test ./...
	cd services/notifications && go test ./...

run-auth:
	cd services/auth && go run ./cmd/auth

run-notifications:
	cd services/notifications && go run ./cmd/notifications
