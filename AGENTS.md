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
   whose root is always a literal `type: "object"` with `properties`.
2. **`<Message>_JsonSchema_WithDefs(defs)` function** - Composition helper.
   Its behaviour follows the message's **mode** (decided by cycle analysis):
   - **inline** (the common case): returns a fresh, complete object schema and
     registers nothing.
   - **`$defs`** (messages on a reference cycle): registers the definition
     under `defs` and returns a `$ref` to it.
3. **`<message>_JsonSchema_build(defs)`** - only for `$defs`-mode messages,
   and **unexported**: the schema body, run once to fill the `$defs` entry
   and once to build an independent root (jsonschema-go resolution requires a
   tree). Not part of the generated API.

```go
// Inline mode (acyclic message): no $ref, no $defs unless a cyclic descendant exists.
func (x *User) JsonSchema() *jsonschema.Schema {
    defs := make(map[string]*jsonschema.Schema)
    root := User_JsonSchema_WithDefs(defs)
    if len(defs) > 0 {
        root.Defs = defs
    }
    return root
}

// $defs mode (message on a cycle): the definition lives under $defs, the root is a
// separately built object whose self-references resolve into it.
func (x *Node) JsonSchema() *jsonschema.Schema {
    defs := make(map[string]*jsonschema.Schema)
    _ = Node_JsonSchema_WithDefs(defs)
    root := Node_JsonSchema_build(defs, false)
    root.Defs = defs
    return root
}
```

**Why hybrid (2026-09):** MCP clients and SDKs do not reliably resolve
`$ref`/`$defs` (python-sdk #2384, typescript-sdk #1175, spec issue #3238); the
ecosystem inlines. Recursion is the only thing inlining cannot express, so
`$defs` is reserved for messages that actually sit on a cycle. Proto forbids
circular imports, so a cycle always lives inside one `.proto` file and the
mode of a message is the same from every file that references it.

---

## Architecture

The plugin separates **deciding** what a schema is from **printing** Go source. The
vocabulary below is used consistently in code and docs (deep-module terms: a
*module* is an interface plus an implementation; a module is *deep* when a small
interface hides a lot of behaviour):

- **Reference walker** (`messageReferences` in `plugin/model.go`) — the one
  definition of "message M refers to message T": every non-ignored field whose
  value is a message (singular, repeated, map value, oneof variant, proto2
  group), free-form well-known types excluded. Both the cycle analysis and the
  collector use it, so an ignored field is neither an edge nor a forced
  dependency.
- **Cycle analysis** (`analyzeCycles` in `plugin/analyze.go`) — Tarjan SCC
  over the graph `messageReferences` defines (nesting is not an edge;
  free-form well-known types are not nodes). Produces the `cycleSet` that
  decides each message's mode: inline (acyclic) or `$defs` (on a cycle).
- **Schema model** (`plugin/model.go`) — the deep module. `buildMessageSchema`
  turns one message plus its options into a complete symbolic model
  (`messageSchemaModel`): every keyword, required-field decision, oneof shape,
  and option override is decided here. The model is the plugin's internal seam:
  tests assert on decided models, not on generated source text. A
  `schemaContext` (file prefix + cycle set) is threaded through the builders.
- **Message identity** (`messageIdentity` in `plugin/model.go`) — answers every
  naming and strategy question about a message exactly once: its `$defs` key,
  generated function base name (and the unexported `_build` name),
  method-vs-standalone-function, and inline-vs-`$defs` mode (`cyclic`).
  `isGoogleType` is consulted here and in `generateFile`'s local/Google split;
  `freeFormJSONType` in `messageReferences` (free-form types never enter the
  graph) and in the field builders (their fields inline as untyped nodes).
- **Value shape** (`buildElement` in `plugin/model.go`) — the single answer to
  "what is the JSON shape of one proto value", shared by array elements and map
  values (message → reference to the target's schema, inline or `$ref` as
  the target decides; free-form WKT → untyped node; enum → integer + values;
  bytes → base64 string; scalar → mapped type).
- **Printer** (`plugin/printer.go`) — a thin adapter from model to Go source.
  It owns literal layout, string escaping, and import qualification
  (`QualifiedGoIdent`), and knows both Go syntactic contexts (statement and
  composite literal). It makes no schema decisions.
- **Collection** (`plugin/collect.go`) — `getMessagesWithForce` walks messages,
  applying generate options and the force logic (below). `collectTargets`
  runs it over every file in the request first, so a file can generate a
  message it defines that a sibling file forced (`forcedLocalMessages`).
- **Orchestration** (`plugin/plugin.go`, `plugin/generate.go`) —
  `plugin.Generate` collects request-wide targets, then
  `Generator.generateFile` runs collect → analyze → build → print per proto
  file.
- **Options extraction** (`plugin/options.go`) — reads the alis proto
  extensions at file/message/field level.

```
protoc invokes plugin
         │
         ▼
   plugin.Generate()                     plugin/plugin.go
         │
         ├── collectTargets()         ──► request-wide target set (cross-file force)
         ▼
 Generator.generateFile()                plugin/generate.go
         │
         ├── getMessagesWithForce()  ──► collect target messages (+ forced deps,
         │                               + forcedLocalMessages from siblings)
         │
         ├── analyzeCycles()         ──► cycleSet: which messages sit on a cycle
         │
         ├── per message:
         │      buildMessageSchema() ──► messageSchemaModel   (decide — model.go)
         │            │
         │            ├── identityFor()        naming + inline/$defs mode
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

- `plugin/model_test.go` and `plugin/analyze_test.go` (in-package) unit-test
  the model and the cycle analysis against the checked-in descriptor set and
  small dynamic descriptors — no protoc, no network, no generated-source
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
| `message`    | `"object"`       | Inline object schema; `$ref` into `$defs` only for messages on a cycle |
| `group` (proto2) | `"object"`   | Same as `message` — protoc-gen-go emits a nested message type; `isMessageField` covers both kinds. Editions files are rejected by protoc (the plugin declares no editions support) |
| `repeated T` | `"array"`        | `items` contains element schema                    |
| `map<K, V>`  | `"object"`       | `additionalProperties` contains value schema       |
| `oneof`      | `null` \| `"object"` | Nested PascalCase wrapper (all messages, Google types included) |

### Message-Typed Fields: Decorated References

Message-typed fields (including oneof message variants) call the target's
`_WithDefs` and **decorate the schema it returns** with the field's comment,
options and constraints. What that call returns is the target's decision:

- **Inline target** (acyclic): a fresh complete object schema. A field
  comment or `title`/`description` option **replaces the message's own title
  and description as a pair** on that copy (the field is more specific, and a
  field-only description must not sit next to the message's own title);
  constraints such as `min_properties` are set on it. Decided by
  `buildRefSiblings` (`replaceMetadata`).
- **`$defs` target** (cyclic): a fresh `&Schema{Ref: ...}`; the same keywords
  become Draft 2020-12 **siblings of `$ref`**, which `google/jsonschema-go`
  (used by the MCP go-sdk) resolves correctly.

```go
// Field comment and/or options decorate the fresh schema the call returns:
schema.Properties["shipping_address"] = Address_JsonSchema_WithDefs(defs)
schema.Properties["shipping_address"].Description = "Shipping address for deliveries"

// In composite-literal contexts (oneof variants) a closure decorates it:
"ContactInfo": func() *jsonschema.Schema {
    s := ContactInfo_JsonSchema_WithDefs(defs)
    s.Description = "Primary contact information"
    return s
}(),
```

This is safe because `_WithDefs` always returns a **fresh** value, never a
definition stored in `defs`. Array/map **elements** that are messages get the
bare returned schema (no decoration); container options apply at the field
root.

**Free-form well-known types.** `google.protobuf.Struct`, `Value` and
`ListValue` are the only well-known types whose Go form implements
`json.Marshaler` with plain-JSON semantics, so fields of those types emit
free-form inline nodes — `{type: object}`, `{type: array}`, and for Value
an explicit `type` list of every JSON type (an empty schema would marshal as the
boolean `true`, which strict clients such as OpenAI and Gemini reject) — with
the field's metadata/options, never a reference. They
generate no functions and are not nodes of the cycle analysis
(`freeFormJSONType`, `plugin/model.go`).

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

**Google types included:** Google types get the same wrappers. The only
Google types with a custom `json.Marshaler` (Struct/Value/ListValue) are
free-form and never reach the oneof machinery; every other Google type is
plain protoc-gen-go output, so `encoding/json` emits the PascalCase wrapper
for it too (pinned by `google.iam.admin.v1.LintPolicyRequest` in common.proto).

### Metadata Emission

`Title`/`Description` keywords are emitted **only when non-empty**, at every
level (message, field, oneof variant). `jsonschema-go` marshals with
`omitempty`, so this changed generated Go cosmetically without changing the
schema JSON.

---

## Google Types

All Google types (any message in a `google.*` package) generate schemas like
user messages (structural, following the same inline/`$defs` rule) — except the
free-form Struct/Value/ListValue above. Since they're imported types (no
methods can be added), the plugin generates **standalone functions**:

```go
// From user.proto — file prefix keeps names unique across files in a package
func user_google_protobuf_Timestamp_JsonSchema() *jsonschema.Schema { ... }
func user_google_protobuf_Timestamp_JsonSchema_WithDefs(defs map[string]*jsonschema.Schema) *jsonschema.Schema { ... }
```

The prefix is derived from the proto file name (`users/v1/admin.proto` → `admin`), sanitised so it can start a Go identifier (`my-d.proto` → `my_d`, `2fa.proto` → `_2fa`; `prefixFromPath`). All naming lives in `messageIdentity` / `googleTypeFunctionName` / `fileNamePrefix` (`plugin/model.go`).

---

## Options System

The plugin uses custom proto options from `go.alis.build/common/alis/open/options` (publicly resolvable; vendored for tests at `third_party/protos/alis/open/options/v1/options.proto`).

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

`generate` is presence-based (`optional bool`): only an explicitly set value
overrides the file-level default. Declaring other message-level options (such
as `oneof` below) does **not** change whether the message generates.

**Force Logic for Dependencies and Nested Messages**: when a message has
`generate = true`, its **field dependencies** and **nested messages** are
**forced to generate** even with explicit `generate = false`, so every
`_WithDefs` call resolves at Go compile time. Implemented in
`getMessagesWithForce()` (`plugin/collect.go`) over `messageReferences`: map
fields force their value message, ignored fields force nothing, free-form
well-known types are never collected. The force crosses files: a dependency
defined in a sibling file of the same protoc run generates in that file
(`collectTargets` + `forcedLocalMessages`). It cannot cross packages — a
`generate = false` message referenced from another package must be targeted
in its own package (documented limitation).

### Message-Level Oneof Groups (`json_schema.oneof`)

Declares mutually exclusive field groups for the schema — the LLM-facing
"pick exactly one request shape" construct (same pattern as
`buf.validate.message.oneof`):

```protobuf
message CheckoutRequest {
  option (alis.open.options.v1.message).json_schema.oneof = {
    fields: ["card", "bank_transfer", "mobile_money"],
    required: true   // exactly one; false/unset = at most one
  };
  Card card = 1;
  BankTransfer bank_transfer = 2;
  MobileMoney mobile_money = 3;
}
```

Semantics (decided in `buildDeclaredOneofGroups`, `plugin/model.go`):

- **Members leave the plain `required` array** — they are conditionally
  required by the group. This is also the clean escape hatch from "all
  singular message fields are required".
- Emitted at the message root: one group → `oneOf` of per-member presence
  branches; several groups → `allOf` of such `oneOf`s. `required: true` omits
  the "none present" branch (exactly-one); otherwise it is included
  (at-most-one).
- Presence branches: ordinary fields → `{required: ["field_name"]}`; members
  of a real proto `oneof` block → the PascalCase wrapper form
  `{required: ["Wrapper"], properties: {"Wrapper": {type: "object", required:
  ["Variant"]}}}` (the wrapper key is always present under encoding/json, null
  when unset).
- Validation (generation-time errors): ≥ 2 members, all names must exist, no
  repeated/map/ignored members, a field in at most one group.
- **Subset narrowing is allowed by design** (2026-08 decision): a group may
  cover only some members of a proto `oneof` — the schema then narrows the
  contract, same philosophy as `enum_int` narrowing a proto enum. With
  `required: true`, a legal proto value that sets an uncovered variant fails
  schema validation; that is the point, not a bug. Don't add a full-coverage
  guard without new evidence.
- Caveat: presence of a *scalar* member with a zero value is invisible to
  `json.Marshal` (omitempty) — groups work best over message-typed fields.

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
`max_properties`, `content_encoding`, `content_media_type`, plus the typed
variants: `enum_string`/`enum_int`/`enum_number`, `deprecated`, `read_only`,
`write_only`, `multiple_of`, `default_string`/`default_int`/`default_number`/
`default_bool`, `examples_string`/`examples_int`/`examples_number`.

Application rules (all decided in `plugin/model.go`):

- Scalar fields: metadata + container + value constraints at the root.
- Arrays/maps: metadata + container constraints at the root; value constraints
  (incl. `enum_*` and `multiple_of`) on the element (`items` /
  `additionalProperties`).
- Message-typed fields and oneof message variants: everything decorates the
  schema the reference returns (see Decorated References): on an inline copy
  a field comment/`title`/`description` replaces the target's own pair and
  constraints are set on the copy; on a `$ref` everything is a sibling.
- **Annotations** (`deprecated`/`read_only`/`write_only`): always on the
  field's schema root — on containers they mark the array/object itself, on
  message fields they decorate the returned schema.
- **Reads are presence-based** (the option fields are proto3 optional):
  `minimum: 0`, `min_items: 0` etc. are expressible. `exclusive_minimum: true`
  emits the `minimum` value as `ExclusiveMinimum` and skips `Minimum`;
  `exclusive_minimum` without `minimum` is a generation-time error. Same for
  maximum.
- **Typed variants are validated at generation time** (errors name the field,
  in `validateFieldOptions`): at most one variant per group; the variant must
  match the value's JSON type (string/bytes → `_string`; integers and proto
  enums → `_int`; float/double → `_number`; bool → `default_bool`);
  `default_*`/`examples_*` only on singular scalar fields; `multiple_of` > 0
  and numeric-only; `enum_int` on a proto enum must be a subset of its
  declared values and **replaces** the auto-emitted full value list; every
  numeric option must be **finite** (protoc accepts `inf`/`-inf`/`nan`
  literals, which have no JSON form and would print as Go identifiers that
  do not compile — checked first, since NaN passes every range comparison).
- `Default` is emitted as `json.RawMessage` (the typed value marshaled at
  generation time); `Enum`/`Examples` as typed `[]any` literals.

---

## Testing

### Layout

```
plugin/model_test.go        In-package model tests (no protoc needed; uses
                            testdata/descriptors/user.pb)
plugin/analyze_test.go      In-package cycle-analysis tests (fixture + dynamic
                            descriptors: self, mutual, map-value, diamond,
                            proto2 group) and the dynamic-descriptor helpers
                            (dynFileCustom: chosen path and syntax)
plugin_test/suite.go        PluginTestSuite: descriptor regeneration (protoc)
                            with checked-in fallback
plugin_test/testutil.go     Shared harness: protoc runner, proto discovery,
                            golden assertions, temp-module helpers, shared
                            schema-validation helper source
plugin_test/plugin_test.go  Generate-level output tests
plugin_test/functions_test.go  Generated-shape pins (oneofs, decorated references)
plugin_test/integration_test.go Golden auto-discovery + compile-and-run tests
plugin_test/recursive_mcp_test.go Recursive types, cross-package reference
                            (xref/v1) + MCP AddTool smoke test
plugin_test/legacy_test.go  proto2 groups compile-and-run test
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
  (alis options, google/iam and transitive deps, and the google/protobuf
  well-known types) are vendored under `third_party/protos/`. The WKTs are
  vendored deliberately: golden files embed their doc comments, and protoc's
  built-in copies vary by protoc version — the explicit `--proto_path` copy
  wins, keeping output identical on every protoc version. No machine-specific
  paths.
- `testdata/descriptors/user.pb` is checked in as the no-protoc fallback for
  the main suite and the in-package model tests. Regenerate it after editing
  any users/v1 proto (any suite run with protoc installed does).
- Fixture roles: `users/v1` (main; force.proto pins force logic incl. the
  cross-file `CrossFileDependency` and the ignored-field case; admin.proto
  references the cyclic `AddressDetails` from a sibling file; ConstraintDemo
  pins the title-only pair rule), `recursive/v1` (self, mutual and map-value
  cycles), `xref/v1` (cross-package reference into a cyclic message),
  `legacy/v1` (proto2 groups), `options_demo/v1` (every field option in every
  position: KeywordShowcase for typed variants, AnnotationShowcase,
  ElementShowcase, DecorationShowcase, SubsetOneofDemo, plus the declared
  oneof groups; each has accept/reject runtime checks in
  `plugin_test/options_demo_test.go`), `no_options/v1`. Adding an option
  means adding it to a showcase message **and** a runtime check there.

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
`schemaTestHelpersSource()` in `plugin_test/testutil.go`, written with
`writeSchemaTestHelpers` — never an embedded copy, so every temp module
enforces the same root-shape and `$ref` rules. Temp modules that
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

Only messages on a reference cycle use `$defs` (decided by `analyzeCycles`).
For those: (1) `_WithDefs` returns early with a `$ref` when the key exists,
(2) otherwise it **reserves the key** (`defs[key] = nil`) before calling the
unexported `_build(defs)` and stores the result, and (3) `JsonSchema()`
builds the root a second time with the same `_build(defs)`: an independent
tree whose self-references resolve into `$defs`. The body is identical in
both modes; only the wrappers differ. Never share nodes between the root and
a definition — `jsonschema-go`'s `Resolve` (called by `mcp.AddTool`) rejects
schemas that do not form a tree. Pinned by `recursive/v1/recursive.proto`
(self, mutual and map-value cycles), `legacy/v1` (a cycle through a proto2
group), users/v1 `AddressDetails` (also referenced from admin.proto), and
`xref/v1` (referenced from another package).

### Multi-File Packages

Each proto file generates schemas only for messages **defined in that file**
(filtered by `msg.Desc.ParentFile().Path()`); messages from sibling files are
referenced by `_WithDefs` name. All files sharing a Go package must be
compiled together. A message a sibling file forced (a `generate = false`
dependency) is generated in its defining file via the request-wide
`collectTargets` set. Across packages nothing is forced: the dependency must
be targeted in its own package.

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
- **Hybrid inline/`$defs` model** (2026-09): acyclic messages inline
  (zero `$ref`/`$defs`); only messages on a cycle live under `$defs`; roots are
  always literal objects; cyclic roots are built as an independent tree.
  Evidence: MCP python-sdk #2384, typescript-sdk #1175 (whose fix is this exact
  shape), spec issue #3238, and jsonschema-go `For[T]` erroring on cycles.
  Rejected: depth-limited unrolling (imprecise, size blow-up) and a runtime
  dereference pass (bypasses the model, costs a walk per call). No option to
  force `$defs` for large shared messages — add only with evidence.
- **Struct/Value/ListValue are free-form** (2026-09): they are the only WKTs
  with custom `MarshalJSON`, so `encoding/json` emits plain JSON for them; the
  structural schema was wrong and was the one WKT cycle. Value spells "any"
  as the full `type` list, never as an empty schema (marshals as `true`).
- **Google oneofs use wrappers too** (2026-09): the flat-oneof exception for
  Google types was justified by custom marshalers; with those types free-form,
  every remaining Google type marshals like a user message, so the exception
  was removed (`google.protobuf.Value`'s flat shape was its only correct
  instance).
- **`_build` is unexported and flagless** (2026-09-04): the cyclic builder
  was briefly an exported `_build(defs, register bool)`; before the v0.3.0
  tag it became `<message>_JsonSchema_build(defs)` — unexported because only
  the message's own two functions call it, and flagless because `_WithDefs`
  can reserve the key itself. Freezing the exported form would have made the
  simpler shape a breaking change.
- **One definition of "reference"** (2026-09-04): the analyzer and the
  collector had drifted (ignored fields were edges in neither, but the
  collector still forced their targets). `messageReferences` is the single
  rule; a message reachable only through an ignored field generates nothing.
- **Force crosses files, not packages** (2026-09-04): a `generate = false`
  message referenced from a sibling file left an undefined `_WithDefs` call
  while the docs promised every reference resolves. The request-wide
  `collectTargets` pass fixes the same-run case; cross-package stays a
  documented limitation (the plugin cannot emit into another package).
- **proto2 groups are message fields** (2026-09-04): `GroupKind` fell
  through to a bare `{type: object}` while protoc-gen-go marshals the nested
  struct. `isMessageField` covers both kinds everywhere. Editions files are
  not supported (no `FEATURE_SUPPORTS_EDITIONS`), so editions' delimited
  encoding never reaches the plugin.
- **Non-finite numeric options are errors** (2026-09-04): protoc accepts
  `inf`/`nan` for double options; `%g` printed them as `+Inf`/`NaN`, which
  is not Go. Rejected in `validateFieldOptions` before any other rule.
- **File prefixes are sanitised** (2026-09-04): `my-d.proto` produced
  `my-d_google_protobuf_…`, which protogen rejected as unparsable.
  `prefixFromPath` mirrors protoc-gen-go's own sanitisation (non-identifier
  runes → `_`, leading digit prefixed).
- **A singular non-`optional` self-reference is unsatisfiable** (noted
  2026-09): proto3-style `required` makes such a schema reject every finite
  document. That is the fixture `users.v1.AddressDetails` by design; real
  protos should mark the closing field `optional`. No generation-time check
  was added.
- **Decoration over allOf** (2026-08, generalised 2026-09): metadata/constraints
  on message fields decorate the returned schema (overrides on inline copies,
  Draft 2020-12 siblings on `$ref`); allOf-wrapping was rejected (uglier, and
  the target consumer — jsonschema-go / MCP — handles siblings).
- **Schemas target `encoding/json` output**, not protojson: snake_case
  properties, PascalCase oneof wrappers, numeric enums.
- **The model is the test surface**: unit tests assert decided models
  in-package; generated-source string matching is reserved for pinning output
  shapes, and goldens pin everything else. Invalid option combinations cannot
  live under `testdata/protos` (they abort generation) — their tests build
  dynamic in-memory descriptors (`dynMessage` in `plugin/model_test.go`).
- **Presence-based option reads** (2026-08): pointer/len presence replaced the
  `!= 0` heuristics. Awakening note: an explicit `minimum: 0` (previously
  silently dropped) now emits, and `exclusive_minimum` without a bound is now
  an error instead of emitting 0.
- **`generate` needs presence** (2026-08): message-level `generate` is
  `optional bool` — without presence, declaring any other message-level option
  (e.g. `json_schema.oneof`) was misread as an explicit `generate = false` and
  silently disabled the message's schema. Requires options module >= v1.8.0.
- **Declared oneof members leave `required`** (2026-08): a field named in a
  `json_schema.oneof` group is conditionally required by the group constraint,
  never by the plain `required` array.

---

## File Locations Quick Reference

| What                          | Where                                                     |
| ----------------------------- | --------------------------------------------------------- |
| Plugin entry point            | `cmd/protoc-gen-go-jsonschema/main.go`                    |
| Orchestration (generateFile)  | `plugin/generate.go`                                      |
| Schema model + identity       | `plugin/model.go`                                         |
| Cycle analysis (inline/$defs) | `plugin/analyze.go` → `analyzeCycles()`                   |
| Printer                       | `plugin/printer.go`                                       |
| Message collection / force    | `plugin/collect.go` → `getMessagesWithForce()`, `collectTargets()` |
| Reference walker              | `plugin/model.go` → `messageReferences()`                 |
| Options extraction            | `plugin/options.go`                                       |
| Model unit tests              | `plugin/model_test.go`, `plugin/analyze_test.go`          |
| Test harness helpers          | `plugin_test/testutil.go`                                 |
| Golden auto-discovery         | `plugin_test/integration_test.go` → `TestGoldenFile`      |
| MCP smoke test                | `plugin_test/recursive_mcp_test.go`                       |
| proto2 groups test            | `plugin_test/legacy_test.go`                              |
| Test protos                   | `testdata/protos/<name>/v1/*.proto`                       |
| Vendored third-party protos   | `third_party/protos/`                                     |
| Golden files                  | `testdata/golden/*.golden`                                |
| CI                            | `.github/workflows/test.yml`                              |
