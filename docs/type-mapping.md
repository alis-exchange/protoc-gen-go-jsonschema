---
title: Type mapping
nav_order: 3
---

# Type mapping

How proto constructs map to JSON Schema. Everything on this page describes the
JSON produced by Go's `encoding/json` for protoc-gen-go types — see
[Compatibility](compatibility.md) for why that matters.

## Property names

Schemas use **proto field names** (snake_case), matching the `json:"..."`
struct tags protoc-gen-go emits:

```protobuf
string first_name = 1;   // property key: "first_name"
```

The exception is oneof wrappers — see [Oneofs](#oneofs) below.

## Scalar types

| Proto type | JSON Schema | Notes |
|---|---|---|
| `string` | `"string"` | |
| `bool` | `"boolean"` | |
| `int32`, `sint32`, `sfixed32`, `uint32`, `fixed32` | `"integer"` | |
| `int64`, `sint64`, `sfixed64`, `uint64`, `fixed64` | `"integer"` | JavaScript loses precision beyond 2⁵³−1; accepted trade-off |
| `float`, `double` | `"number"` | |
| `bytes` | `"string"` | with `contentEncoding: "base64"` |
| enum | `"integer"` | with `enum: [0, 1, 2, ...]` — see below |

An unsupported field kind aborts generation with an error naming the field —
never a silently broken schema.

## Enums

`encoding/json` marshals proto enums as their **numeric values**, so schemas
declare `"type": "integer"` with an `enum` listing every declared value:

```json
{ "type": "integer", "enum": [0, 1, 2] }
```

You can narrow the allowed subset with the `enum_int` field option — see the
[options reference](options.md#typed-value-options).

## Messages

A message-typed field is **inlined**: the referenced message's complete object
schema appears in place. Field comments and field options on the field
override the message's own `title`/`description` on that copy:

```json
{
  "properties": {
    "address": {
      "type": "object",
      "description": "Shipping address.",
      "properties": { "street": { "type": "string" }, "city": { "type": "string" } },
      "required": ["city"]
    }
  }
}
```

Only messages on a **reference cycle** (self-referencing, or mutually
recursive through any field kind) are different: their definition lives under
the root's `$defs`, keyed by full proto name, and every reference to them is a
`$ref` with the field's metadata as sibling keywords:

```json
{
  "type": "object",
  "properties": {
    "children": {
      "type": "array",
      "items": { "$ref": "#/$defs/tree.v1.Node" }
    }
  },
  "$defs": { "tree.v1.Node": { "type": "object", "properties": { "...": {} } } }
}
```

The root is always a literal `type: "object"` with `properties`, never a
`$ref` (see [Generated code](generated-code.md)).

## Repeated fields

`repeated T` becomes an array whose `items` carry T's schema:

```json
{ "type": "array", "items": { "type": "string" } }
```

Value-level options (`pattern`, `enum_string`, `multiple_of`, ...) apply to
the **items**; container options (`min_items`, `max_items`, `unique_items`)
and annotations (`deprecated`, ...) apply to the **array itself**.

## Maps

`map<K, V>` becomes an object with `additionalProperties` carrying V's schema.
JSON object keys are always strings, so non-string proto keys get a
`propertyNames` pattern:

| Proto key type | `propertyNames` pattern |
|---|---|
| `string` | none |
| integer kinds | `"^-?[0-9]+$"` |
| `bool` | `"^(true|false)$"` |

## Oneofs

protoc-gen-go emits no `json` struct tags for oneof wrapper types, so
`encoding/json` produces **nested PascalCase wrappers** — and the schemas
match that shape:

```json
{ "Identifier": { "Email": "user@example.com" }, "ContactPreference": null }
```

- The wrapper property key is the oneof's Go name (`Identifier`).
- Each variant is an object with a single required PascalCase key (`Email`).
- The wrapper key is **always present**: `encoding/json` emits it as `null`
  when the oneof is unset, and the schema accepts that via a null branch.

Fields *inside* referenced messages stay snake_case as usual.

A proto3 `optional` field (a synthetic oneof) is **not** wrapped — it stays a
flat property and is simply omitted from `required`.

## Required fields

A field lands in `required` unless it is `optional`, a oneof member, repeated,
a map, or a member of a declared [oneof group](oneof-groups.md). This states
the **proto3 contract**: singular fields (scalars and messages alike) are
required unless marked `optional`.

> **Caveat:** protoc-gen-go tags every field `omitempty`, so `json.Marshal`
> omits zero-valued scalars and nil messages. A marshaled zero-heavy value can
> fail validation against its own schema. The schema states the contract for
> producers; see [Compatibility](compatibility.md#required-vs-omitempty).

## Well-known and Google types

Google types (`google.protobuf.*`, `google.type.*`, `google.iam.*`, ...) are
generated as standalone functions in the referencing file (methods can't be
added to imported types), with a file-name prefix keeping names unique:

```go
func user_google_protobuf_Timestamp_JsonSchema() *jsonschema.Schema
```

Their oneofs keep **flat** properties with root-level `oneOf`/`allOf`
constraints (e.g. `google.iam.admin.v1.LintPolicyRequest`'s `condition`),
because those types implement custom `json.Marshaler` methods with proto JSON
semantics.

`google.protobuf.Struct`, `google.protobuf.Value` and
`google.protobuf.ListValue` are the exception: their Go types marshal as plain
JSON under `encoding/json`, so fields of those types map to free-form schemas
— `{"type": "object"}`, `{}` (any JSON value) and `{"type": "array"}` — with
the field's own comment and options. No functions are generated for them.
