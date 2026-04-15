## Summary

<!-- What problem does this solve? What changed at a high level? -->

## User-visible changes

<!-- If this fixes a bug or adds behavior consumers see, note it here for release notes. Otherwise: N/A -->

## Testing

<!-- How did you verify this? -->

- [ ] `go test -tags=plugintest ./plugin_test/...`
- [ ] If generator output changed: ran `go test -tags=plugintest ./plugin_test/... -update` and reviewed golden diffs under `testdata/golden/`

## Documentation

- [ ] **AGENTS.md** — I updated it for a **significant** change to plugin behavior, options, generated output, or contributor workflows, **or** I did not need to (small fix / internal-only / no documented behavior change). See [CONTRIBUTING.md](https://github.com/alis-exchange/protoc-gen-go-jsonschema/blob/main/.github/CONTRIBUTING.md).

## Notes for reviewers

<!-- Optional: design decisions, follow-ups, risk areas -->
