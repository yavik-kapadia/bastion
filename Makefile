.PHONY: build build-frontend build-backend clean test dev

# Build everything: frontend first, then Go binary with embedded assets.
build: build-frontend build-backend

build-frontend:
	cd frontend && npm ci && npm run build
	rm -rf cmd/bastion/frontend
	cp -r frontend/build cmd/bastion/frontend

# Build Go binary with embedded frontend.
build-backend:
	CGO_ENABLED=0 go build -o bastion ./cmd/bastion

# Quick backend-only build (no frontend embedding, uses nofrontend tag).
build-api:
	CGO_ENABLED=0 go build -tags nofrontend -o bastion ./cmd/bastion

# Run tests (uses nofrontend tag so no frontend build needed).
test:
	go test -tags nofrontend ./...

# Clean build artifacts.
clean:
	rm -f bastion
	rm -rf cmd/bastion/frontend frontend/build frontend/.svelte-kit

# Run development server (backend only, with Vite proxy for frontend).
dev:
	go run -tags nofrontend ./cmd/bastion &
	cd frontend && npm run dev
