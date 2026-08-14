---
title: Generated code
nav_order: 6
---

# Generated code

For each targeted proto file the plugin writes one
`<name>_jsonschema.pb.go` into the same Go package as the regular
protoc-gen-go output. Its only runtime dependency is
`github.com/google/jsonschema-go/jsonschema` (plus `encoding/json` when
defaults are used).

## The API

Each message gets two functions:

```go
// Public entry point — a complete, self-contained schema.
func (x *User) JsonSchema() *jsonschema.Schema

// Composition helper — populates a shared definitions map, returns a $ref.
func User_JsonSchema_WithDefs(defs map[string]*jsonschema.Schema) *jsonschema.Schema
```

`JsonSchema()` is what you normally call. `_WithDefs` exists so schemas can
reference each other and so you can compose several messages into one
definitions map yourself.

## Ref-as-root

`JsonSchema()` returns a `$ref` wrapper with everything bundled under `$defs`:

```go
func (x *User) JsonSchema() *jsonschema.Schema {
    defs := make(map[string]*jsonschema.Schema)
    _ = User_JsonSchema_WithDefs(defs)
    root := &jsonschema.Schema{Ref: "#/$defs/users.v1.User", Type: "object"}
    root.Defs = defs
    return root
}
```

Why this shape:

- **No pointer cycles.** The root is a fresh wrapper, not the definition
  itself, so `json.Marshal` of a recursive schema can't stack-overflow.
- **Recursive types resolve.** A message whose fields reference itself points
  at `#/$defs/...`, and that definition always exists — definitions are
  registered *before* their fields are populated.
- **Self-contained.** Every transitively referenced message (and Google type)
  is present under `$defs`, keyed by its full proto name.

`_WithDefs` always returns a **fresh** `&Schema{Ref: ...}` value — never the
stored definition — which is also what makes `$ref` sibling decoration safe:

```go
schema.Properties["shipping_address"] = Address_JsonSchema_WithDefs(defs)
schema.Properties["shipping_address"].Description = "Shipping address for deliveries"
```

## Multi-file packages

Each proto file generates schemas only for messages **defined in that file**;
messages from sibling files in the same Go package are referenced by their
`_WithDefs` function. That is why all files sharing a Go package must be
compiled together — the cross-file calls must resolve at Go compile time.
Cross-package references are import-qualified automatically.

## Google types

Imported Google types can't receive methods, so they generate standalone
functions in the file that references them, prefixed with the proto file's
name to stay unique across a package:

```go
func user_google_protobuf_Timestamp_JsonSchema() *jsonschema.Schema
func user_google_protobuf_Timestamp_JsonSchema_WithDefs(defs map[string]*jsonschema.Schema) *jsonschema.Schema
```

You rarely call these directly — they exist to serve `$ref`s from your own
messages' schemas.

## Stability guarantees

- Generated function names and signatures are stable across plugin versions;
  regenerating with a newer plugin never breaks callers of `JsonSchema()` or
  `_WithDefs`.
- Output is deterministic: properties follow field order, oneof constraint
  branches follow declaration order, Google-type oneof groups sort by name.
- Nothing is emitted for a file whose messages are all untargeted — no empty
  files.
- Invalid options abort generation with a field-naming error rather than
  emitting a broken schema.
