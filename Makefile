# airom-rules maintenance helpers. See MAINTAINING.md.

# Lint AND test every pack with airom's own validator (same as CI).
# Requires: pip install airom
.PHONY: lint
lint:
	@fail=0; \
	for pack in $$(find rules -name '*.yaml' -not -path '*/testdata/*' | sort); do \
		airom rules lint "$$pack" || fail=1; \
		airom rules test "$$pack" || fail=1; \
	done; \
	exit $$fail

# Build an unsigned bundle into dist/ for local inspection (Go 1.25+).
.PHONY: bundle
bundle:
	go run ./tools/bundle -rules rules -version v0.0.0-local -out dist -unsigned

# Ranked "what to add next" gap report over a corpus of cloned repos.
# Usage: make candidates CORPUS=~/ai-corpus
.PHONY: candidates
candidates:
	@test -n "$(CORPUS)" || { echo "set CORPUS=<dir of cloned repos>"; exit 1; }
	go run ./tools/candidates -corpus $(CORPUS)

# Remove local build output.
.PHONY: clean
clean:
	rm -rf dist
