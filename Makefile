.PHONY: build test lint vet diagram run

build:        ## build the CLI into ./bin
	go build -o bin/triage ./cmd/triage

test:         ## run every test with the race detector
	go test -race -count=1 ./...

vet:
	go vet ./...

lint: vet     ## golangci-lint (brew install golangci-lint)
	golangci-lint run ./...

diagram:      ## regenerate docs/graph.mmd from the real graph
	go run ./cmd/triage diagram -out docs/graph.mmd > /dev/null

run:          ## make run ISSUE=owner/repo#1
	go run ./cmd/triage run $(ISSUE)
