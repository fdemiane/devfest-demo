# Metro Paradox — Money Transfer Demo

A minimal Go project for demonstrating AIP-guided API design, deterministic linting/codegen, and test-driven agent implementation.

## Current State (Before Agent)

- `transfer/transfer.go` contains a placeholder `TransferMoney` method that only prints `"Money transfered"`.
- `transfer/transfer_test.go` contains failing unit tests covering:
  - **Conservation of money** (`sum(balances after) == sum(balances before)`)
  - **Idempotency** (same `request_id` returns the same result and moves money once, AIP-155)
  - **Concurrency / Serializability** (parallel transfers match some sequential execution, no negative balances)
  - **Atomicity** (mid-transfer failures leave balances unchanged)
  - **Validation & edge cases** (insufficient funds, zero/negative amounts, currency mismatch, self-transfer, missing `request_id`, missing accounts, fractional `nanos` arithmetic)

## Demo Workflow

1. **Create `.proto` schema** in `proto/` following Google AIPs (AIP-122, AIP-133, AIP-143, AIP-155).
2. **Lint** with Google's `api-linter`:
   ```bash
   make lint
   ```
3. **Generate Go & gRPC code** with `protoc`:
   ```bash
   make proto
   ```
4. **Implement** the transfer service so all unit tests pass:
   ```bash
   make test
   ```

