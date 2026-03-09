.PHONY: build test cover clean sqlc

build:
	go build -o commentcrawl ./cmd/commentcrawl

test:
	go test ./... -count=1

cover:
	go test ./... -coverprofile=cover.out
	go tool cover -func=cover.out

clean:
	rm -f commentcrawl cover.out

sqlc:
	cd store && sqlc generate
