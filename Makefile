GOBIN := $(shell go env GOPATH)/bin
PROTOS := $(shell find proto -name '*.proto')
PROTOC := $(shell PATH="$(GOBIN):$$PATH" command -v protoc 2>/dev/null || echo "$(GOBIN)/buf alpha protoc")

.PHONY: test lint proto run-server stop-server run-client

run-server:
	go run ./cmd/server

stop-server:
	@pids=$$(lsof -ti :50051); [ -n "$$pids" ] && kill $$pids || echo "No server running on :50051"

run-client:
	go run ./cmd/server/client.go

test:
	go test -v ./...

lint:
	cd proto && PATH="$(GOBIN):$$PATH" $(GOBIN)/api-linter --set-exit-status -I ../third_party/googleapis --config=../api-linter.yaml $(patsubst proto/%,%,$(PROTOS))

proto:
	rm -rf gen && mkdir -p gen
	PATH="$(GOBIN):$$PATH" $(PROTOC) -I proto -I third_party/googleapis --go_out=gen --go_opt=paths=source_relative --go-grpc_out=gen --go-grpc_opt=paths=source_relative $(PROTOS)
