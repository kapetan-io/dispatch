.DEFAULT_GOAL := help

.PHONY: help
help: ## List the targets
	@awk 'BEGIN {FS = ":.*## "} /^[a-z-]+:.*## / {printf "%-12s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: install
install: ## Install the dispatch CLI
	go install ./cmd/dispatch

.PHONY: test
test: ## Run the fast suite, no race detector, no slow tests
	go test ./...

.PHONY: test-slow
test-slow: ## Run the whole suite including tests tagged tests_slow
	go test -tags tests_slow ./...

.PHONY: test-race
test-race: ## Run the fast suite under the race detector
	go test -race ./...

.PHONY: test-drift
test-drift: ## Check the fakes against the installed tools (CI skips this)
	@# Strip credentials so a drift test cannot reach a GitHub or Linear account.
	GH_CONFIG_DIR=$$(mktemp -d) env -u GH_TOKEN -u GITHUB_TOKEN -u LINEAR_API_KEY go test -tags drift ./internal/...

.PHONY: test-report
test-report: ## Report tests over the speed budget, tagged ones included
	@go run ./cmd/slowtests -tags tests_slow

.PHONY: ci
ci: ## Run what CI runs: vet, the whole suite, shellcheck
	go vet ./...
	go test -tags tests_slow ./...
	shellcheck -S warning bin/dispatch-launch scripts/shims/claude
