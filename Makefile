.PHONY: build web build-ui dev-api dev-web test vet lint run hooks docker docker-run release-snapshot coverage
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
docker:          ; docker buildx build --platform linux/amd64 -t drumdrop:local .
docker-run:      ; docker run --rm -p 3737:8080 -e DRUMDROP_API_TOKEN=devtoken -v drumdrop-config:/config -v $(PWD)/downloads:/downloads drumdrop:local
release-snapshot:; goreleaser release --snapshot --clean --skip=publish

# The reports SonarQube reads (sonar-project.properties names each one; `sonar-scan` runs
# this target before it uploads): the Go cover profile and `go test -json` stream, then
# the web lcov and test-execution report. A failing test fails the target, so a scan never
# uploads stale numbers. -json sends the test output to the report, so on a failure the
# failed events are printed here.
coverage:
	go test -count=1 -json -coverprofile=coverage.out ./... > go-test-report.json || \
	  { grep '"Action":"fail"' go-test-report.json >&2; \
	    echo "make coverage: Go tests failed; full output in go-test-report.json" >&2; exit 1; }
	go tool cover -func=coverage.out | tail -n 1
	cd web && npm run test:coverage
