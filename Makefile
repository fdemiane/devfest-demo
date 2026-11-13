.PHONY: test lint proto

test:
	go test -v ./...

lint:
	api-linter --config=api-linter.yaml proto/**/*.proto

proto:
	protoc -I proto --go_out=gen --go_opt=paths=source_relative --go-grpc_out=gen --go-grpc_opt=paths=source_relative proto/**/*.proto

