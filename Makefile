.PHONY: help test race cover bench lint vet fmt tidy check clean

help:
	@grep -E '^[a-z-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}'

test: ## run tests with the race detector and coverage
	go test -race -cover -coverprofile coverage.out ./...
	go tool cover -func coverage.out

race: ## run tests repeatedly under the race detector
	go test -race -count=20 -cpu=1,2,8 ./...

cover: test ## open the HTML coverage report
	go tool cover -html coverage.out -o coverage.html

bench:
	go test -run '^$$' -bench . -benchmem ./...

lint:
	golangci-lint run --timeout 5m

vet:
	go vet ./...

fmt:
	go fmt ./...

tidy:
	go mod tidy

check: fmt vet lint test ## everything CI runs

clean:
	rm -f coverage.out coverage.html
