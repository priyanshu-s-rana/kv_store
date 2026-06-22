.PHONY: setup fmt

# Run once after cloning to activate the pre-commit hook.
setup:
	git config core.hooksPath .githooks
	@echo "hooks active: gofmt will run automatically on every commit"

# Manual format — useful in CI or before opening a PR.
fmt:
	gofmt -w .
