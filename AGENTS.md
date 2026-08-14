# AI Agent Guide for protoc-gen-go-jsonschema

This document provides comprehensive information for AI agents and LLMs working with this codebase.

## ⚠️ IMPORTANT: Documentation Maintenance

**LLMs and AI agents MUST update this document when making significant changes to the plugin.**

Significant changes include:

- New features or capabilities
- Changes to message generation logic
- New options or option behaviors
- Bug fixes that change behavior
- New test patterns or testing approaches
- Changes to the code generation output format

**This document is the single source of truth for understanding how the plugin works.**

## Project Overview

**protoc-gen-go-jsonschema** is a Protocol Buffers compiler plugin that generates Go code for creating JSON Schema (Draft 2020-12) representations of proto messages at runtime.

- **Repository**: `github.com/alis-exchange/protoc-gen-go-jsonschema`
- **Language**: Go
- **Purpose**: Generate `JsonSchema()` methods for proto messages
- **Output**: JSON Schema Draft 2020-12 compliant schemas
- **Key Dependency**: `github.com/google/jsonschema-go/jsonschema`
- **Primary consumers**: agents and MCP tools that marshal with `json.Marshal` (not `protojson`) and register schemas via `mcp.AddTool`

For each targeted proto message, the plugin generates:

1. **`JsonSchema()` method** - Public API that returns a complete `*jsonschema.Schema`
2. **`<Message>_JsonSchema_WithDefs()` function** - Internal helper for recursive schema building with shared definitions

```go
// Generated code example (ref-as-root pattern)
func (x *User) JsonSchema() *jsonschema.Schema {
    defs := make(map[string]*jsonschema.Schema)
    _ = User_JsonSchema_WithDefs(defs)
    root := &jsonschema.Schema{Ref: "#/$defs/package.User", Type: "object"}
    root.Defs = defs
    return root
}
```

---

## Architecture

The plugin separates **deciding** what a schema is from **printing** Go source. The
vocabulary below is used consistently in code and docs (deep-module terms: a
*module* is an interface plus an implementation; a module is *deep* when a small
interface hides a lot of behaviour):

- **Schema model** (`plugin/model.go`) — the deep module. `buildMessageSchema`
  turns one message plus its options into a complete symbolic model
  (`messageSchemaModel`): every keyword, required-field decision, oneof shape,
  and option override is decided here. The model is the plugin's internal seam:
  tests assert on decided models, not on generated source text.
- **Message identity** (`messageIdentity` in `plugin/model.go`) — answers every
  naming and strategy question about a message exactly once: its `$defs` key,
  generated function base name, method-vs-standalone-function, and
  flat-vs-wrapped oneof strategy. The `isGoogleType` predicate is consulted
  only here.
- **Value shape** (`buildElement` in `plugin/model.go`) — the single answer to
  "what is the JSON shape of one proto value", shared by array elements and map
  values (message → `$ref`, enum → integer + values, bytes → base64 string,
  scalar → mapped type).
- **Printer** (`plugin/printer.go`) — a thin adapter from model to Go source.
  It owns literal layout, string escaping, and import qualification
  (`QualifiedGoIdent`), and knows both Go syntactic contexts (statement and
  composite literal). It makes no schema decisions.
- **Collection** (`plugin/collect.go`) — `getMessagesWithForce` walks messages,
  applying generate options and the force logic (below).
- **Orchestration** (`plugin/generate.go`) — `Generator.generateFile` runs
  collect → build → print per proto file.
- **Options extraction** (`plugin/options.go`) — reads the alis proto
  extensions at file/message/field level.

```
protoc invokes plugin
         │
         ▼
   plugin.Generate()                     plugin/plugin.go
         │
         ▼
 Generator.generateFile()                plugin/generate.go
         │
         ├── getMessagesWithForce()  ──► collect target messages (+ forced deps)
         │
         ├── per message:
         │      buildMessageSchema() ──► messageSchemaModel   (decide — model.go)
         │            │
         │            ├── identityFor()        naming + oneof strategy
         │            ├── buildFieldProperty() field → ref or inline node
         │            ├── buildElement()       value shape for arrays/maps
         │            └── buildOneofWrapper()  nested wrapper shape
         │
         └── schemaPrinter.printMessageSchema()  (print — printer.go)
                    │
                    ▼
          Generated *_jsonschema.pb.go
```

### Testing the seam

- `plugin/model_test.go` (in-package) unit-tests the model against the
  checked-in descriptor set — no protoc, no network, no generated-source
  string matching.
- `plugin_test/` pins the generated output: golden files for **every** proto
  under `testdata/protos` (auto-discovered), string-shape tests for oneof/ref
  patterns, and compile-and-run integration tests (including an MCP
  `AddTool` smoke test) that build temp Go modules.

---

## Type Mapping Reference

### Field Names

Generated schemas use **proto field names** (snake_case) instead of JSON names (camelCase). This is because agents and MCP tools typically use `json.Marshal` instead of `protojson.Marshal`.

The `getFieldName()` helper returns the proto field name directly via `field.Desc.Name()`.

### Scalar Types

| Proto Type                    | JSON Schema Type | Additional Constraints            |
| ----------------------------- | ---------------- | --------------------------------- |
| `string`                      | `"string"`       | —                                 |
| `bool`                        | `"boolean"`      | —                                 |
| `int32`, `sint32`, `sfixed32` | `"integer"`      | —                                 |
| `uint32`, `fixed32`           | `"integer"`      | —                                 |
| `int64`, `sint64`, `sfixed64` | `"integer"`      | —                                 |
| `uint64`, `fixed64`           | `"integer"`      | —                                 |
| `float`                       | `"number"`       | —                                 |
| `double`                      | `"number"`       | —                                 |
| `bytes`                       | `"string"`       | `contentEncoding: "base64"`       |
| `enum`                        | `"integer"`      | `enum: [0, 1, 2, ...]` (numeric values for encoding/json) |

**Note**: 64-bit integers are mapped to `"integer"` for simplicity; JavaScript precision limits beyond 2^53-1 are accepted.

An **unsupported field kind aborts generation with an error** naming the field
(it previously emitted a silent `Type: ""`).

### Complex Types

| Proto Type   | JSON Schema Type | Structure                                          |
| ------------ | ---------------- | -------------------------------------------------- |
| `message`    | `"object"`       | `$ref` to the message's def, optionally with sibling keywords |
| `repeated T` | `"array"`        | `items` contains element schema                    |
| `map<K, V>`  | `"object"`       | `additionalProperties` contains value schema       |
| `oneof`      | `null` \| `"object"` | Nested PascalCase wrapper (user types); flat (Google types) |

### Message-Typed Fields: $ref with Sibling Keywords

Message-typed fields (including oneof message variants) emit a `$ref` to the
target's definition **plus any field-level metadata and option constraints as
Draft 2020-12 sibling keywords**. Sibling keywords next to `$ref` are
applicable in 2020-12 (the ref-as-root pattern already relies on this), and
`google/jsonschema-go` — which the MCP go-sdk uses — resolves them correctly.

```go
// Field comment and/or options emit as siblings on the fresh $ref schema:
schema.Properties["shipping_address"] = Address_JsonSchema_WithDefs(defs)
schema.Properties["shipping_address"].Description = "Shipping address for deliveries"

// In composite-literal contexts (oneof variants) a closure decorates the ref:
"ContactInfo": func() *jsonschema.Schema {
    s := ContactInfo_JsonSchema_WithDefs(defs)
    s.Description = "Primary contact information"
    return s
}(),
```

This is safe because `_WithDefs` always returns a **fresh** `&Schema{Ref: ...}`
wrapper, never the definition stored in `defs`.

⚠️ **Compatibility notes**:

- Draft-07-era consumers ignore siblings of `$ref`; they see the same schema as
  before (the metadata is simply dropped, as it always was for them).
- Constraint options (`min_properties` etc.) on message-typed fields were
  **silently dropped** before this feature; they now take effect. Documents
  that validated before may be rejected if such dormant options exist.
- Array/map **elements** that are messages still emit a bare `$ref` (no
  sibling support there); container options apply at the field root as before.

### Required Fields

A field is added to `required` only if **all** of the following are true:

- Not in a `oneof` group
- Not marked with the `optional` keyword
- Not `repeated` (array) and not a `map`

This is deliberately the **proto3 contract**: singular fields (scalars *and*
message fields) are required unless `optional`. Use `optional` on a message
field to take it out of `required`.

⚠️ **Known, accepted tension**: protoc-gen-go tags every field `omitempty`, so
`json.Marshal` omits zero-valued scalars and nil message fields. A marshalled
zero-heavy value can therefore fail validation against its own schema. This was
considered and **deliberately kept** (2026-08): the schema states the intended
contract for producers (agents generating input), not the marshal round-trip.
Architecture reviews should not re-suggest presence-based or explicit-only
`required` without new evidence.

### Map Key Handling

Map keys are always strings in JSON. Non-string proto keys use `propertyNames` validation:

| Proto Key Type         | propertyNames Pattern |
| ---------------------- | --------------------- |
| `string`               | (none)                |
| `int32`, `int64`, etc. | `"^-?[0-9]+$"`        |
| `bool`                 | `"^(true\|false)$"`   |

### Oneof Handling

Oneof fields use **nested PascalCase wrappers** to match `encoding/json` behavior: protoc-gen-go does not add `json` struct tags to oneof interface fields or wrapper structs, so `encoding/json` uses Go's exported field names.

The generated shape is an outer `oneOf` of a **null branch** (unset oneof — `encoding/json` always emits the wrapper key, null when unset) and an **object branch** holding the variant `oneOf`:

```json
{
  "Identifier": {"Email": "user@example.com"},
  "ContactPreference": null
}
```

**Naming rules:**

| Layer | Source | JSON key |
|-------|--------|----------|
| Oneof wrapper property | `oneof.GoName` | PascalCase (e.g., `Identifier`) |
| Variant key inside wrapper | `field.GoName` | PascalCase (e.g., `Email`) |
| Fields inside nested messages | `field.Desc.Name()` | snake_case |

**Google types exception:** Google types keep flat oneof behavior (root-level
`OneOf`/`AllOf` with a none-set branch) because they implement custom
`json.Marshaler` methods with proto JSON semantics. The strategy is decided by
`messageIdentity`; the printer knows nothing about oneofs' meaning.

### Metadata Emission

`Title`/`Description` keywords are emitted **only when non-empty**, at every
level (message, field, oneof variant). `jsonschema-go` marshals with
`omitempty`, so this changed generated Go cosmetically without changing the
schema JSON.

---

## Google Types

All Google types (any message in a `google.*` package) generate schemas with `$ref` definitions. Since they're imported types (no methods can be added), the plugin generates **standalone functions**:

```go
// From user.proto — file prefix keeps names unique across files in a package
func user_google_protobuf_Timestamp_JsonSchema() *jsonschema.Schema { ... }
func user_google_protobuf_Timestamp_JsonSchema_WithDefs(defs map[string]*jsonschema.Schema) *jsonschema.Schema { ... }
```

The prefix is derived from the proto file name (`users/v1/admin.proto` → `admin`). All naming lives in `messageIdentity` / `googleTypeFunctionName` / `fileNamePrefix` (`plugin/model.go`).

---

## Options System

The plugin uses custom proto options from `open.alis.services/protobuf` (vendored for tests at `third_party/protos/alis/open/options/v1/options.proto`).

### File-Level Options

```protobuf
import "alis/open/options/v1/options.proto";
option (alis.open.options.v1.file).json_schema.generate = true;
```

### Message-Level Options

```protobuf
message User {
  option (alis.open.options.v1.message).json_schema.generate = true;
}
```

**Force Logic for Dependencies and Nested Messages**: when a message has
`generate = true`, its **field dependencies** and **nested messages** are
**forced to generate** even with explicit `generate = false`, so `$ref`
pointers always resolve. Implemented in `getMessagesWithForce()`
(`plugin/collect.go`); map fields force their value message (field 2 of the
synthetic entry).

### Field-Level Options

```protobuf
string email = 1 [(alis.open.options.v1.field).json_schema = {
  format: "email"
  title: "Email Address"
  min_length: 5
}];
```

Available: `ignore`, `title`, `description`, `format`, `pattern`, `minimum`,
`maximum`, `exclusive_minimum`, `exclusive_maximum`, `min_length`,
`max_length`, `min_items`, `max_items`, `unique_items`, `min_properties`,
`max_properties`, `content_encoding`, `content_media_type`.

Application rules (all decided in `plugin/model.go`):

- Scalar fields: metadata + container + value constraints at the root.
- Arrays/maps: metadata + container constraints at the root; value constraints
  on the element (`items` / `additionalProperties`).
- Message-typed fields and oneof message variants: everything emits as `$ref`
  **siblings** (see above).
- Exclusive bounds: `exclusive_minimum: true` emits `ExclusiveMinimum` with the
  `minimum` value (even 0) and skips `Minimum`; a bare non-zero `minimum` emits
  `Minimum`. Proto3 zero values make an explicit `minimum: 0` without the
  exclusive flag indistinguishable from unset. Same for maximum.

---

## Testing

### Layout

```
plugin/model_test.go        In-package model tests (no protoc needed; uses
                            testdata/descriptors/user.pb)
plugin_test/suite.go        PluginTestSuite: descriptor regeneration (protoc)
                            with checked-in fallback
plugin_test/testutil.go     Shared harness: protoc runner, proto discovery,
                            golden assertions, temp-module helpers, shared
                            schema-validation helper source
plugin_test/plugin_test.go  Generate-level output tests
plugin_test/functions_test.go  Generated-shape pins (oneofs, $ref siblings)
plugin_test/integration_test.go Golden auto-discovery + compile-and-run tests
plugin_test/recursive_mcp_test.go Recursive types + MCP AddTool smoke test
```

There is **no build tag**: `go test ./...` runs everything. Tests needing
`protoc`, `protoc-gen-go`, or the network **skip themselves** when the tool is
missing (or with `-short`). CI (`.github/workflows/test.yml`) installs the
tools and runs the full suite on every PR and push to main.

### Proto fixtures and goldens

- Test protos live under `testdata/protos/<name>/v1/`. **Every directory is
  auto-discovered** by `TestGoldenFile`; files in one directory are compiled
  together (multi-file Go package support). Adding a proto automatically
  requires a golden (`-update` creates it); goldens whose protos disappeared
  fail the test.
- All proto imports resolve from the repo itself: third-party protos
  (alis options, google/iam and transitive deps) are vendored under
  `third_party/protos/`. No machine-specific paths.
- `testdata/descriptors/user.pb` is checked in as the no-protoc fallback for
  the main suite and the in-package model tests.

```shell
go test ./...                      # everything (self-skipping)
go test ./plugin/                  # model tests only (fast, no protoc)
go test ./plugin_test/... -update  # refresh golden files
go test -short ./...               # skip compile-and-run integration tests
```

### Temp-module integration tests

Compile-and-run tests write generated code into a temp Go module, `go mod
tidy`, and run embedded tests there. Shared validation helpers
(`ValidateSchema`, `collectRefs`, ...) come from one source —
`schemaTestHelpersSource()` in `plugin_test/testutil.go`. Temp modules that
build real `.pb.go` files need the alis Go registry; `alisModuleEnv()`
provides it (proxy.golang.org first — the alis registry answers 401, not 404,
for modules it does not host, and `go` only falls through on 404/410; the alis
module itself is declared explicitly in temp `go.mod` files because module
discovery probes subpaths that 401).

The MCP smoke test (`recursive_mcp_test.go`) generates real protoc-gen-go
types for the recursive proto and registers `JsonSchema()` input/output
schemas with `mcp.AddTool` — the plugin's primary consumer path.

---

## Development Commands

```shell
go build ./...                                    # build
go install ./cmd/protoc-gen-go-jsonschema         # install to GOPATH/bin
./build.sh v1.0.0                                 # cross-platform release build

protoc --plugin=protoc-gen-go-jsonschema=./protoc-gen-go-jsonschema \
       --go-jsonschema_out=. \
       --proto_path=testdata/protos --proto_path=third_party/protos \
       your.proto
```

---

## Common Issues and Solutions

### Circular References and Recursive Types

Handled by (1) registering the schema in `defs` **before** its fields are
populated, (2) `_WithDefs` returning early with a `$ref` when the key exists,
and (3) the **ref-as-root** pattern: `JsonSchema()` returns a `$ref` wrapper
with `root.Defs = defs`, so `root != defs[key]` (no pointer cycle on marshal)
and recursive `$refs` resolve. Pinned by `recursive/v1/recursive.proto` tests.

### Multi-File Packages

Each proto file generates schemas only for messages **defined in that file**
(filtered by `msg.Desc.ParentFile().Path()`); messages from sibling files are
referenced by `_WithDefs` name. All files sharing a Go package must be
compiled together.

### Changing Generation Behavior

1. Decide in `plugin/model.go` (never in the printer).
2. Add/adjust an in-package model test.
3. Run `go test ./plugin_test/... -update` and **review the golden diff** — it
   is the change's visible surface.
4. Update this document.

Behavior-preserving refactors must leave goldens byte-identical (no `-update`).

---

## Design Decisions (do not re-litigate without new evidence)

- **Required is proto3-style** (2026-08): singular fields required unless
  `optional`, including message fields. Presence-based and explicit-signal
  alternatives were considered and rejected; the `omitempty` round-trip caveat
  is documented under Required Fields. There is no `json_schema.required`
  option upstream (the options package doc comment advertising one is a doc
  bug).
- **$ref siblings over allOf** (2026-08): metadata/constraints on message
  fields emit as Draft 2020-12 siblings of `$ref`; allOf-wrapping was rejected
  (uglier, and the target consumer — jsonschema-go / MCP — handles siblings).
- **Schemas target `encoding/json` output**, not protojson: snake_case
  properties, PascalCase oneof wrappers, numeric enums.
- **The model is the test surface**: unit tests assert decided models
  in-package; generated-source string matching is reserved for pinning output
  shapes, and goldens pin everything else.

---

## File Locations Quick Reference

| What                          | Where                                                     |
| ----------------------------- | --------------------------------------------------------- |
| Plugin entry point            | `cmd/protoc-gen-go-jsonschema/main.go`                    |
| Orchestration (generateFile)  | `plugin/generate.go`                                      |
| Schema model + identity       | `plugin/model.go`                                         |
| Printer                       | `plugin/printer.go`                                       |
| Message collection / force    | `plugin/collect.go` → `getMessagesWithForce()`            |
| Options extraction            | `plugin/options.go`                                       |
| Model unit tests              | `plugin/model_test.go`                                    |
| Test harness helpers          | `plugin_test/testutil.go`                                 |
| Golden auto-discovery         | `plugin_test/integration_test.go` → `TestGoldenFile`      |
| MCP smoke test                | `plugin_test/recursive_mcp_test.go`                       |
| Test protos                   | `testdata/protos/<name>/v1/*.proto`                       |
| Vendored third-party protos   | `third_party/protos/`                                     |
| Golden files                  | `testdata/golden/*.golden`                                |
| CI                            | `.github/workflows/test.yml`                              |
