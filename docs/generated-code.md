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

// Composition helper — returns the message's schema: a complete inline object,
// or (recursive messages only) a $ref after registering the definition in defs.
func User_JsonSchema_WithDefs(defs map[string]*jsonschema.Schema) *jsonschema.Schema
```

`JsonSchema()` is what you normally call. `_WithDefs` exists so schemas can
reference each other: an inline message returns its schema and writes nothing
to `defs`; a recursive message registers itself there and returns a `$ref`.

Messages on a reference cycle also get an **unexported**
`<message>_JsonSchema_build(defs)` helper holding the schema body. The two
functions above call it; it is not part of the API.

## Inline by default, `$defs` for cycles

The plugin analyses the message graph before generating. A message that is
**not on a reference cycle** inlines: its `_WithDefs` builds and returns a
fresh, complete object schema, and `JsonSchema()` hands that object back as
the root. A proto without recursion therefore produces **no `$ref` and no
`$defs` anywhere**:

```go
func (x *User) JsonSchema() *jsonschema.Schema {
    defs := make(map[string]*jsonschema.Schema)
    root := User_JsonSchema_WithDefs(defs)
    if len(defs) > 0 {
        root.Defs = defs // only when a recursive descendant registered one
    }
    return root
}
```

A message **on a cycle** — self-referencing, or mutually recursive through
any field kind — is the one thing inlining cannot express. Such messages live
under `$defs` and are reached through `$ref`; nothing else is:

```go
func (x *Node) JsonSchema() *jsonschema.Schema {
    defs := make(map[string]*jsonschema.Schema)
    _ = Node_JsonSchema_WithDefs(defs)   // registers Node (and its cycle) under defs
    root := node_JsonSchema_build(defs)  // an independent copy for the root
    root.Defs = defs
    return root
}
```

Why this shape:

- **MCP-clean roots.** The root is always a literal `type: "object"` with
  `properties` — the shape the MCP spec mandates and the shape clients read
  directly. `$ref` appears only where recursion actually happens.
- **Recursive types resolve.** `_WithDefs` reserves the definition's key
  *before* building its body, so references back to it resolve to a `$ref`
  instead of recursing forever.
- **No shared nodes.** The root of a recursive message is built separately
  from its `$defs` entry; `jsonschema-go`'s `Resolve` (which `mcp.AddTool`
  calls) requires the schema to be a tree, and `json.Marshal` cannot cycle.
- **Self-contained.** Every recursive message reachable from the root is
  present under `$defs`, keyed by its full proto name.

Proto forbids circular imports, so a cycle always lives inside one `.proto`
file: the mode of a message is intrinsic and identical from every file that
references it.

`_WithDefs` always returns a **fresh** value — an inline object or a
`&Schema{Ref: ...}` — never a stored definition, which is what makes field
decoration safe:

```go
schema.Properties["shipping_address"] = Address_JsonSchema_WithDefs(defs)
schema.Properties["shipping_address"].Description = "Shipping address for deliveries"
```

On an inline copy a field comment or `title`/`description` option **replaces**
the message's own title and description as a pair; on a `$ref` they are Draft
2020-12 sibling keywords.

## Multi-file packages

Each proto file generates schemas only for messages **defined in that file**;
messages from sibling files in the same Go package are referenced by their
`_WithDefs` function. That is why all files sharing a Go package must be
compiled together — the cross-file calls must resolve at Go compile time.

Dependencies are forced across files: a message that opts out with
`generate = false` but is referenced from a sibling file in the same protoc
run generates anyway, in the file that defines it. Fields marked
`ignore: true` are not references and force nothing.

Cross-package references are import-qualified automatically, but the
dependency must be generated in its own package: a `generate = false` message
referenced from another package leaves an unresolved `_WithDefs` call.

## Google types

Imported Google types can't receive methods, so they generate standalone
functions in the file that references them, prefixed with the proto file's
name to stay unique across a package (characters that cannot appear in a Go
identifier become `_`, and a leading digit gets one prepended: `my-d.proto`
→ `my_d_…`):

```go
func user_google_protobuf_Timestamp_JsonSchema() *jsonschema.Schema
func user_google_protobuf_Timestamp_JsonSchema_WithDefs(defs map[string]*jsonschema.Schema) *jsonschema.Schema
```

You rarely call these directly — they exist to serve your own messages'
schemas. `google.protobuf.Struct`, `Value` and `ListValue` generate nothing:
their Go types marshal as plain JSON, so fields of those types emit free-form
nodes — `{"type": "object"}`, `{"type": "array"}`, and for `Value` an explicit `type` list of every JSON type.

## Stability guarantees

- `JsonSchema()` and `<Message>_JsonSchema_WithDefs(defs)` are the generated
  API: their names and signatures are stable, and packages generated by
  different plugin versions link and compose — a caller never needs to know
  whether a `_WithDefs` call returns an inline object or a `$ref`. Everything
  else in a generated file (the unexported `_build` helpers, the Google-type
  functions for free-form types that v0.3.0 removed) is an implementation
  detail.
- The schema **shape** can change between minor versions. Every such change
  is listed in [Compatibility](compatibility.md).
- Output is deterministic: properties follow field order; oneof wrappers,
  their variants and constraint branches follow declaration order.
- Nothing is emitted for a file whose messages are all untargeted — no empty
  files.
- Invalid options — non-finite numbers such as `inf` or `nan` included —
  abort generation with a field-naming error rather than emitting a broken
  schema.
