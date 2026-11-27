# Project Rules

## Coding Standards
- Follow **SOLID** principles (Single Responsibility, Open/Closed, Liskov Substitution, Interface Segregation, Dependency Inversion).
- Do not add unnecessary code comments in Go files.
- Follow Google AIP (https://google.aip.dev/) when defining `.proto` schemas.
- Implement the gRPC service directly on `Service` in `transfer/transfer.go` and register it in `cmd/server/main.go`. Do not create extra files or modify `*_test.go` files.

## Unit Tests
- Name unit tests as `Test<MethodName>_<behavior>_<expectation>` (e.g., `TestParseConfig_ValidInput_ReturnsConfig`).
- Aim for **full test coverage**.
- Keep each unit test focused on a **single responsibility** (one behavior or property per test).
- **Parameterize tests** (e.g., table-driven tests) whenever two or more tests share the same structure.
- Keep assertions **simple**.
- Structure every unit test into 3 stages, with a comment before each stage:
  - `// Given`
  - `// When`
  - `// Then`
