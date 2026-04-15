# Contributing to protoc-gen-go-jsonschema

Thank you for helping improve this project. Clear contribution guidelines save time for both maintainers and contributors. GitHub surfaces this file on the repository **Contributing** tab and when opening issues or pull requests; see [Setting guidelines for repository contributors](https://docs.github.com/en/communities/setting-up-your-project-for-healthy-contributions/setting-guidelines-for-repository-contributors) for how that works.

## Code of conduct

Be respectful and constructive. Assume good intent, keep feedback specific, and focus on the problem or the code.

## What to contribute

Useful contributions include bug reports with reproduction steps, failing tests, documentation fixes, and focused pull requests that solve one problem at a time.

## Before you start

- Read [README.md](README.md) for what the plugin does and how consumers use it.
- For architecture, generation flow, options, and testing patterns, see [AGENTS.md](AGENTS.md).

## Development setup

### Requirements

- **Go** version matching [go.mod](go.mod) (currently `go 1.25.0` or newer within that toolchain policy).
- **protoc** on your `PATH` if you run integration-style tests or regenerate descriptor sets (the test suite shells out to `protoc` where needed).

### Private module access

This module depends on `open.alis.services/protobuf` from Artifact Registry. Configure Go the same way as in the README so `go mod download` and `go test` resolve dependencies:

```shell
export GOPROXY=https://europe-west1-go.pkg.dev/alis-org-777777/openprotos-go,https://proxy.golang.org,direct
export GONOPROXY=github.com/alis-exchange/protoc-gen-go-jsonschema
export GONOSUMDB=open.alis.services/protobuf
```

Then:

```shell
git clone https://github.com/alis-exchange/protoc-gen-go-jsonschema.git
cd protoc-gen-go-jsonschema
go mod download
```

### Build

```shell
go build ./...
go build -o protoc-gen-go-jsonschema ./cmd/protoc-gen-go-jsonschema
```

## Tests

Most tests live under `plugin_test/` and require the **`plugintest`** build tag so production builds do not compile test-only helpers.

```shell
go test -tags=plugintest ./plugin_test/...
```

Optional:

```shell
go test -tags=plugintest -race ./plugin_test/...
```

### Golden files

Some tests compare generated output to files under `testdata/golden/`. If you intentionally change generator output, refresh goldens and review the diff:

```shell
go test -tags=plugintest ./plugin_test/... -update
```

Commit updated `.golden` files together with the code change.

### Package layout (quick reference)

| Area                             | Path                                    |
| -------------------------------- | --------------------------------------- |
| Plugin entry                     | `cmd/protoc-gen-go-jsonschema/`         |
| Core generator                   | `plugin/` (`plugin.go`, `functions.go`) |
| Tests                            | `plugin_test/`                          |
| Fixtures / descriptors / goldens | `testdata/`                             |

## Making a change

1. **Prefer small PRs** — one logical change per pull request when possible.
2. **Add or extend tests** for behavior you fix or introduce (unit tests in `plugin_test/`, integration/golden where output shape matters).
3. **Run the full test command** above before opening a PR.
4. **Documentation** — update **AGENTS.md** when the change is **significant** for people working on or integrating the plugin (for example: new or changed generation behavior, options, generated output shape, test fixtures or workflows). Small fixes, internal refactors, or changes that do not affect documented behavior do **not** require an AGENTS.md update.

## Pull requests

A good PR usually includes:

- A short description of the problem and what you changed.
- Test updates and, if applicable, golden updates (`-update`).
- No unrelated refactors or formatting-only churn in files you did not need to touch.

If the change is user-visible (bug fix or feature), note it in the PR body so maintainers can decide whether release notes or a version bump are needed.

## Issues

Helpful bug reports include:

- Go and `protoc` versions (if relevant).
- Minimal proto snippet or steps that reproduce the wrong schema or generated Go.
- Expected vs actual behavior (paste generated code or schema if it helps).

Feature requests are welcome; describe the use case (for example MCP validation, a specific proto pattern, or a new option) so maintainers can weigh design trade-offs.

## License

By contributing, you agree your contributions are licensed under the same terms as the project — see [LICENSE](LICENSE).
