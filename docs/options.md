---
title: Options reference
nav_order: 4
---

# Options reference

All options live in `alis/open/options/v1/options.proto`
(Go module `go.alis.build/common/alis/open/options`). Options marked
**v0.2.0+** require protoc-gen-go-jsonschema v0.2.0 or newer — older plugin
versions silently ignore them.

## File-level

```protobuf
option (alis.open.options.v1.file).json_schema.generate = true;
```

| Option | Type | Effect |
|---|---|---|
| `generate` | `bool` | Generate schemas for every message in this file, unless a message explicitly opts out. |

## Message-level

```protobuf
message User {
  option (alis.open.options.v1.message).json_schema.generate = true;
}
```

| Option | Type | Effect |
|---|---|---|
| `generate` | `optional bool` | Explicitly enables or disables generation for this message, overriding the file default. Unset means "inherit" — declaring other message options (like `oneof`) does **not** change whether the message generates. |
| `oneof` | repeated group | Declares mutually exclusive field groups — see [Oneof groups](oneof-groups.md). **v0.2.0+** |

**Dependencies are always generated.** When a message generates, its
message-typed field dependencies and nested messages generate too — even with
an explicit `generate = false` — so `$ref` pointers always resolve.

## Field-level

```protobuf
string email = 1 [(alis.open.options.v1.field).json_schema = {
  format: "email",
  title: "Primary Email",
  min_length: 5
}];
```

### Where options land

| Field shape | Metadata + annotations | Container constraints | Value constraints |
|---|---|---|---|
| Singular scalar | field root | — | field root |
| `repeated T` | array root | array root | `items` |
| `map<K, V>` | object root | object root | `additionalProperties` |
| Message-typed | `$ref` siblings | `$ref` siblings | `$ref` siblings |

*Value constraints* are `format`, `pattern`, `content_encoding`,
`content_media_type`, bounds, lengths, `enum_*`, `multiple_of`.
*Container constraints* are `min_items`, `max_items`, `unique_items`,
`min_properties`, `max_properties`. *Annotations* are `deprecated`,
`read_only`, `write_only`.

### Metadata

| Option | JSON Schema keyword | Notes |
|---|---|---|
| `title` | `title` | Overrides the title derived from the field's comment |
| `description` | `description` | Overrides the comment-derived description |
| `ignore` | — | Excludes the field from the schema entirely (no property, never required) |

Without options, the field's leading comment supplies the metadata: a
paragraph break splits it into `title` (first paragraph) and `description`
(rest); otherwise the whole comment is the `description`.

### String constraints

| Option | JSON Schema keyword | Notes |
|---|---|---|
| `pattern` | `pattern` | Regular expression |
| `format` | `format` | `email`, `uuid`, `uri`, `date-time`, ... (annotation for most validators) |
| `min_length` / `max_length` | `minLength` / `maxLength` | |
| `content_encoding` | `contentEncoding` | `bytes` fields default to `"base64"`; this overrides |
| `content_media_type` | `contentMediaType` | e.g. `application/json` |

### Numeric constraints

| Option | JSON Schema keyword | Notes |
|---|---|---|
| `minimum` / `maximum` | `minimum` / `maximum` | Inclusive bounds. Reads are presence-based: an explicit `minimum: 0` is emitted. |
| `exclusive_minimum` / `exclusive_maximum` | `exclusiveMinimum` / `exclusiveMaximum` | When true, the bound value is emitted as the exclusive keyword instead of the inclusive one. Setting the flag without its bound is a generation-time error. |
| `multiple_of` | `multipleOf` | Numeric fields only; must be > 0. **v0.2.0+** |

### Container constraints

| Option | JSON Schema keyword | Applies to |
|---|---|---|
| `min_items` / `max_items` | `minItems` / `maxItems` | repeated fields |
| `unique_items` | `uniqueItems` | repeated fields |
| `min_properties` / `max_properties` | `minProperties` / `maxProperties` | map fields |

### Annotations — v0.2.0+

| Option | JSON Schema keyword | Notes |
|---|---|---|
| `deprecated` | `deprecated` | On containers, marks the array/object itself — not its elements |
| `read_only` | `readOnly` | |
| `write_only` | `writeOnly` | |

Annotations work on every field shape, including message-typed fields (they
ride the `$ref` as siblings).

### Typed value options — v0.2.0+

JSON Schema's `enum`, `default`, and `examples` accept any JSON value, which
proto options can't express directly — so each comes in typed variants. Use
the variant matching the field's JSON type:

| Variant | Applies to |
|---|---|
| `enum_string`, `default_string`, `examples_string` | `string`, `bytes` |
| `enum_int`, `default_int`, `examples_int` | integer kinds **and proto enums** |
| `enum_number`, `default_number`, `examples_number` | `float`, `double` |
| `default_bool` | `bool` |

```protobuf
string mode = 3 [(alis.open.options.v1.field).json_schema = {
  enum_string: ["compact", "detailed"],
  default_string: "compact",
  examples_string: ["compact"]
}];

// Narrow a proto enum to a subset of its declared values:
Status status = 9 [(alis.open.options.v1.field).json_schema = {
  enum_int: [1, 2],   // replaces the auto-emitted full value list
  default_int: 1
}];
```

Rules, enforced at generation time:

- At most one variant per group may be set.
- The variant must match the field's JSON type (for repeated/map fields, the
  element/value type governs `enum_*` and `multiple_of`).
- `default_*` and `examples_*` are valid on **singular scalar fields only** —
  a repeated, map, or message-typed field can't express its default with a
  scalar variant.
- `enum_int` on a proto enum field must be a **subset** of the enum's declared
  values, and replaces the auto-emitted list.
- `enum_int`/`default_int` are `int64` and cannot express `uint64` values
  above 2⁶³−1.

## Generation-time errors

Invalid option combinations **abort generation with an error naming the
field** — never a silently wrong schema. Examples:

```text
field acme.v1.M.mode: at most one enum_* variant may be set
field acme.v1.M.count: the default_* variant for JSON type "string" does not match the field's JSON type "integer"
field acme.v1.M.tags: default_* is only valid on singular scalar fields
field acme.v1.M.step: multiple_of must be greater than 0
field acme.v1.M.rate: exclusive_minimum requires minimum to be set
field acme.v1.M.status: enum_int value 5 is not a declared value of enum acme.v1.Status
```
