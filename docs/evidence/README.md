# Verification

Run `make sdk-install sdk-test` to exercise the Node SDK adapter. Run `make test-unit` for the Go suite without a database, and `make test-db && make gate` for the database-backed suite. The test database uses its own port and disposable storage.

The React client has a separate build and test suite. Run `make web-install web-build web-unit` from the repository root.

These tests include deterministic model responses. To exercise a configured model on four synthetic CSV rows and inspect the output, follow the [checked-file example](../../examples/sdk-checked-work/README.md).
