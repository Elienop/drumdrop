.PHONY: build web build-ui dev-api dev-web test vet lint run hooks
build:    ; go build -o dist/drumdrop ./cmd/drumdrop
web:      ; cd web && npm ci && npm run build
build-ui: web ; go build -tags webui -o dist/drumdrop ./cmd/drumdrop
dev-api:  ; go run ./cmd/drumdrop serve
dev-web:  ; cd web && npm run dev
test:     ; go test ./...
vet:      ; go vet ./...
lint:     ; gofmt -l . | tee /dev/stderr | (! read) && go vet ./...
run:      ; go run ./cmd/drumdrop
hooks:    ; git config core.hooksPath scripts/hooks && echo "✓ git hooks enabled (scripts/hooks): commit-msg + pre-push"
