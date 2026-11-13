# Project Rules

## Coding Standards
- Follow **SOLID** principles (Single Responsibility, Open/Closed, Liskov Substitution, Interface Segregation, Dependency Inversion).
- Do not add unnecessary code comments.

## Unit Tests
- Name unit tests as `Test<MethodName>_<behavior>_<expectation>` (e.g., `TestTransferMoney_ValidRequest_UpdatesBalances`).
- Aim for **full test coverage**.
- Keep each unit test focused on a **single responsibility** (one behavior or property per test).
- **Parameterize tests** (e.g., table-driven tests) whenever two or more tests share the same structure.
- Keep assertions **simple**.
- Structure every unit test into 3 stages, with a comment before each stage:
  - `// Given`
  - `// When`
  - `// Then`

