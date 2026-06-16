.PHONY: test test-integration bench vet

# Fast pass: pure-Go tests, no DuckDB required.
test:
	go test -race -count=1 ./...

# Integration pass: real DuckDB + local GeoParquet fixture.
# Requires the spatial extension to be available (Docker build env or autoloaded).
test-integration:
	go test -race -count=1 -tags=integration ./...

# Benchmarks (integration build tag required for DB benchmarks).
bench:
	go test -bench=. -benchmem -tags=integration ./internal/db/... ./internal/cql2/...

vet:
	go vet ./...
