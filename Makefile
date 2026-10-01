.PHONY: run test data build deploy
run:
	set -a; [ -f .env ] && . ./.env; set +a; go run ./cmd/offsite
test:
	go vet ./... && go test ./...
data:
	scripts/crawl.sh
build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/offsite ./cmd/offsite
deploy:
	scripts/deploy.sh
