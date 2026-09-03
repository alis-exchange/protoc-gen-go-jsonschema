---
title: Options reference
nav_order: 4
---

# Options reference
{: .no_toc }

All options live in `alis/open/options/v1/options.proto`
(Go module `go.alis.build/common/alis/open/options`). Options marked
**v0.2.0+** require protoc-gen-go-jsonschema v0.2.0 or newer — older plugin
versions silently ignore them.

Every example below shows the option **as used in a proto** and the **schema
JSON it produces**. The outputs are taken verbatim from this repository's own
test fixtures ([`options_demo.proto`](https://github.com/alis-exchange/protoc-gen-go-jsonschema/blob/main/testdata/protos/options_demo/v1/options_demo.proto)),
so they can't drift from the real generator.

## Table of contents
{: .no_toc .text-delta }

1. TOC
{:toc}

---

## File-level options

```protobuf
import "alis/open/options/v1/options.proto";

option (alis.open.options.v1.file).json_schema.generate = true;
```

| Option | Type | Effect |
|---|---|---|
| `generate` | `bool` | Generate schemas for every message in this file, unless a message explicitly opts out. |

## Message-level options

### `generate`

```protobuf
message User {
  option (alis.open.options.v1.message).json_schema.generate = true;
}
```

| Value | Effect |
|---|---|
| `true` | Generate a schema for this message even if the file default is off |
| `false` | Skip this message even if the file default is on |
| unset | Inherit the file default — declaring *other* message options (like `oneof`) does not change whether the message generates |

**Dependencies are always generated.** When a message generates, its
message-typed field dependencies and nested messages generate too — even with
an explicit `generate = false` — so every reference resolves.

### `oneof` — v0.2.0+

Declares mutually exclusive field groups, enforced at the schema root:

```protobuf
message CheckoutRequest {
  option (alis.open.options.v1.message).json_schema.oneof = {
    fields: ["card", "bank_transfer", "mobile_money"],
    required: true   // exactly one must be set
  };
  option (alis.open.options.v1.message).json_schema.oneof = {
    fields: ["promo_code", "gift_card_code"]   // at most one may be set
  };
  ...
}
```

```json
"allOf": [
  { "oneOf": [
      { "required": ["card"] },
      { "required": ["bank_transfer"] },
      { "required": ["mobile_money"] }
  ]},
  { "oneOf": [
      { "required": ["promo_code"] },
      { "required": ["gift_card_code"] },
      { "not": { "anyOf": [
          { "required": ["promo_code"] },
          { "required": ["gift_card_code"] }
      ]}}
  ]}
]
```

Group members leave the plain `required` array. Full semantics — including
members of real proto `oneof` blocks and subset narrowing — on the
[Oneof groups](oneof-groups.md) page.

---

## Field-level options

```protobuf
string email = 1 [(alis.open.options.v1.field).json_schema = {
  format: "email",
  title: "Primary Email"
}];
```

### Where options land

| Field shape | Metadata + annotations | Container constraints | Value constraints |
|---|---|---|---|
| Singular scalar | field root | — | field root |
| `repeated T` | array root | array root | `items` |
| `map<K, V>` | object root | object root | `additionalProperties` |
| Message-typed | inline copy (replaces the message's own) | inline copy | inline copy |

For a message on a reference cycle the field is a `$ref` instead, and every
keyword above rides it as a sibling.

*Value constraints:* `format`, `pattern`, `content_encoding`,
`content_media_type`, bounds, lengths, `enum_*`, `multiple_of`.
*Container constraints:* `min_items`, `max_items`, `unique_items`,
`min_properties`, `max_properties`.
*Annotations:* `deprecated`, `read_only`, `write_only`.

### `title` and `description`

Override the metadata derived from the field's leading comment:

```protobuf
// This comment would be the description...
string email = 1 [(alis.open.options.v1.field).json_schema = {
  title: "Primary Email",
  description: "...but the option wins."
}];
```

```json
"email": {
  "type": "string",
  "title": "Primary Email",
  "description": "...but the option wins."
}
```

Without options, the comment supplies the metadata: a blank line splits it
into `title` (first paragraph) and `description` (rest); with no blank line
the whole comment is the `description`.

### `ignore`

Removes the field from the schema entirely — no property, never in
`required`:

```protobuf
string internal_state = 9 [(alis.open.options.v1.field).json_schema = {
  ignore: true
}];
```

```json
{ "properties": { }, "required": [] }   // internal_state appears nowhere
```

### `pattern` and `format`

```protobuf
// The human-readable, singular name of the item.
string name = 1 [(alis.open.options.v1.field).json_schema = {
  pattern: "^[A-Z]([A-Za-z_]{0,61}[a-z])+$"
}];

string contact = 2 [(alis.open.options.v1.field).json_schema = {
  format: "email"
}];
```

```json
"name":    { "type": "string", "pattern": "^[A-Z]([A-Za-z_]{0,61}[a-z])+$" },
"contact": { "type": "string", "format": "email" }
```

`format` is an annotation for most validators (`email`, `uuid`, `uri`,
`date-time`, ...); `pattern` is enforced. On repeated/map fields both apply to
each element/value.

### `min_length` and `max_length`

```protobuf
string password = 4 [(alis.open.options.v1.field).json_schema = {
  min_length: 8,
  max_length: 128
}];
```

```json
"password": { "type": "string", "minLength": 8, "maxLength": 128 }
```

### `content_encoding` and `content_media_type`

`bytes` fields default to `contentEncoding: "base64"`; the options override or
extend that:

```protobuf
bytes attachment = 5 [(alis.open.options.v1.field).json_schema = {
  content_media_type: "application/pdf"
}];
```

```json
"attachment": {
  "type": "string",
  "contentEncoding": "base64",
  "contentMediaType": "application/pdf"
}
```

### `minimum` and `maximum`

```protobuf
// Card expiry year.
int64 expiry_year = 1 [(alis.open.options.v1.field).json_schema = {
  minimum: 2000,
  maximum: 2100
}];
```

```json
"expiry_year": {
  "type": "integer",
  "description": "Card expiry year.",
  "minimum": 2000,
  "maximum": 2100
}
```

Reads are presence-based: an explicit `minimum: 0` is emitted (it is not
confused with "unset").

### `exclusive_minimum` and `exclusive_maximum`

When the flag is true, the bound value is emitted as the *exclusive* keyword
instead of the inclusive one — Draft 2020-12 exclusive bounds are numbers, not
booleans:

```protobuf
// Amount in cents; must be greater than 0.
int64 amount_cents = 1 [(alis.open.options.v1.field).json_schema = {
  minimum: 0,
  exclusive_minimum: true,
  maximum: 500000
}];
```

```json
"amount_cents": {
  "type": "integer",
  "description": "Amount in cents; must be greater than 0.",
  "maximum": 500000,
  "exclusiveMinimum": 0
}
```

Setting the flag without its bound is a generation-time error.

### `multiple_of` — v0.2.0+

```protobuf
// Step size in items.
int64 step = 5 [(alis.open.options.v1.field).json_schema = {
  multiple_of: 5
}];
```

```json
"step": { "type": "integer", "description": "Step size in items.", "multipleOf": 5 }
```

Numeric fields only; the value must be greater than 0. `7` fails validation,
`10` passes.

### `min_items`, `max_items`, `unique_items`

Array constraints land on the array root; value constraints land on `items`
(both shown here — `enum_string` constrains the elements):

```protobuf
repeated string tags = 8 [(alis.open.options.v1.field).json_schema = {
  enum_string: ["a", "b", "c"],
  min_items: 0,
  unique_items: true
}];
```

```json
"tags": {
  "type": "array",
  "items": { "type": "string", "enum": ["a", "b", "c"] },
  "minItems": 0,
  "uniqueItems": true
}
```

### `min_properties` and `max_properties`

Map constraints land on the object root; value constraints land on
`additionalProperties`:

```protobuf
map<string, string> labels = 6 [(alis.open.options.v1.field).json_schema = {
  min_properties: 1,
  max_properties: 10
}];
```

```json
"labels": {
  "type": "object",
  "additionalProperties": { "type": "string" },
  "minProperties": 1,
  "maxProperties": 10
}
```

Non-string map keys additionally get a `propertyNames` pattern — see
[Type mapping](type-mapping.md#maps).

### `deprecated`, `read_only`, `write_only` — v0.2.0+

Annotations always sit on the field's schema root. On a repeated/map field
they mark the array/object itself, never its elements:

```protobuf
// Server-assigned entity tag.
string etag = 7 [(alis.open.options.v1.field).json_schema = {
  read_only: true
}];

// Legacy identifier, kept for backwards compatibility.
string legacy_id = 6 [(alis.open.options.v1.field).json_schema = {
  deprecated: true,
  write_only: true
}];
```

```json
"etag": {
  "type": "string",
  "description": "Server-assigned entity tag.",
  "readOnly": true
},
"legacy_id": {
  "type": "string",
  "description": "Legacy identifier, kept for backwards compatibility.",
  "deprecated": true,
  "writeOnly": true
}
```

They also work on message-typed fields, decorating the inlined message schema:

```protobuf
Card legacy_card = 10 [(alis.open.options.v1.field).json_schema = {
  deprecated: true,
  description: "Use CheckoutRequest.card instead."
}];
```

```json
"legacy_card": {
  "type": "object",
  "description": "Use CheckoutRequest.card instead.",
  "deprecated": true,
  "properties": { "expiry_year": { "type": "integer", "description": "Card expiry year.", "minimum": 2000, "maximum": 2100 } },
  "required": ["expiry_year"]
}
```

### `enum_string`, `enum_int`, `enum_number` — v0.2.0+

Restrict a field to a fixed set. Use the variant matching the field's JSON
type:

```protobuf
// Output mode.
string mode = 3 [(alis.open.options.v1.field).json_schema = {
  enum_string: ["compact", "detailed"]
}];

// Sampling rate.
double rate = 2 [(alis.open.options.v1.field).json_schema = {
  enum_number: [0.25, 0.5, 1]
}];
```

```json
"mode": { "type": "string", "description": "Output mode.", "enum": ["compact", "detailed"] },
"rate": { "type": "number", "description": "Sampling rate.", "enum": [0.25, 0.5, 1] }
```

On a **proto enum** field, `enum_int` narrows the auto-emitted value list to a
subset (values must be declared on the enum):

```protobuf
enum Status {
  STATUS_UNSPECIFIED = 0;
  STATUS_ACTIVE = 1;
  STATUS_INACTIVE = 2;
}

// Status narrowed to a subset of the enum's declared values.
Status status = 9 [(alis.open.options.v1.field).json_schema = {
  enum_int: [1, 2]   // replaces the auto-emitted [0, 1, 2]
}];
```

```json
"status": {
  "type": "integer",
  "description": "Status narrowed to a subset of the enum's declared values.",
  "enum": [1, 2]
}
```

On repeated/map fields the set applies to each element/value (see
[`tags`](#min_items-max_items-unique_items) above).

### `default_string`, `default_int`, `default_number`, `default_bool` — v0.2.0+

Declare the field's default. Singular scalar fields only; use the variant
matching the field's JSON type:

```protobuf
string mode = 3 [(alis.open.options.v1.field).json_schema = {
  default_string: "compact"
}];

// Verbose flag.
bool verbose = 4 [(alis.open.options.v1.field).json_schema = {
  default_bool: true
}];
```

```json
"mode":    { "type": "string", "default": "compact" },
"verbose": { "type": "boolean", "description": "Verbose flag.", "default": true }
```

### `examples_string`, `examples_int`, `examples_number` — v0.2.0+

Example values, singular scalar fields only:

```protobuf
// Weekday index.
int32 weekday = 1 [(alis.open.options.v1.field).json_schema = {
  enum_int: [0, 1, 2, 3, 4, 5, 6],
  default_int: 1,
  examples_int: [1, 5]
}];
```

```json
"weekday": {
  "type": "integer",
  "description": "Weekday index.",
  "default": 1,
  "examples": [1, 5],
  "enum": [0, 1, 2, 3, 4, 5, 6]
}
```

(There is deliberately no `examples_bool` — a boolean example carries no
information.)

### Message-typed fields

The referenced message is inlined, and the field's comment and options
replace its own `title`/`description` on that copy:

```protobuf
// Pay with a saved card.
Card card = 1;
```

```json
"card": {
  "type": "object",
  "description": "Pay with a saved card.",
  "properties": { "expiry_year": { "type": "integer", "description": "Card expiry year.", "minimum": 2000, "maximum": 2100 } },
  "required": ["expiry_year"]
}
```

(For a message on a reference cycle the field is a `$ref` into `$defs` and the
same keywords ride it as Draft 2020-12 siblings.) The referenced message's own
schema carries the structure; `default_*`, `examples_*`, and `enum_*` are not
applicable here (generation-time error).

---

## Putting it together

One message using most field options, and its complete real output:

```protobuf
// KeywordShowcase exercises every new field option on happy paths.
message KeywordShowcase {
  // Weekday index.
  int32 weekday = 1 [(alis.open.options.v1.field).json_schema = {
    enum_int: [0, 1, 2, 3, 4, 5, 6], default_int: 1, examples_int: [1, 5]
  }];
  // Sampling rate.
  double rate = 2 [(alis.open.options.v1.field).json_schema = {
    enum_number: [0.25, 0.5, 1], default_number: 0.5, examples_number: [0.25, 1]
  }];
  // Output mode.
  string mode = 3 [(alis.open.options.v1.field).json_schema = {
    enum_string: ["compact", "detailed"], default_string: "compact", examples_string: ["compact"]
  }];
  // Verbose flag.
  bool verbose = 4 [(alis.open.options.v1.field).json_schema = { default_bool: true }];
  // Step size in items.
  int64 step = 5 [(alis.open.options.v1.field).json_schema = { multiple_of: 5 }];
  // Legacy identifier, kept for backwards compatibility.
  string legacy_id = 6 [(alis.open.options.v1.field).json_schema = {
    deprecated: true, write_only: true
  }];
  // Server-assigned entity tag.
  string etag = 7 [(alis.open.options.v1.field).json_schema = { read_only: true }];
  repeated string tags = 8 [(alis.open.options.v1.field).json_schema = {
    enum_string: ["a", "b", "c"], deprecated: true, min_items: 0
  }];
  // Status narrowed to a subset of the enum's declared values.
  Status status = 9 [(alis.open.options.v1.field).json_schema = {
    enum_int: [1, 2], default_int: 1
  }];
  // Superseded card reference.
  Card legacy_card = 10 [(alis.open.options.v1.field).json_schema = {
    deprecated: true, description: "Use CheckoutRequest.card instead."
  }];

  enum Status {
    STATUS_UNSPECIFIED = 0;
    STATUS_ACTIVE = 1;
    STATUS_INACTIVE = 2;
  }
}
```

`(&KeywordShowcase{}).JsonSchema()` marshals to:

```json
{
  "type": "object",
  "properties": {
    "etag": {
      "type": "string",
      "description": "Server-assigned entity tag.",
      "readOnly": true
    },
    "legacy_card": {
      "type": "object",
      "properties": {
        "expiry_year": {
          "type": "integer",
          "description": "Card expiry year.",
          "minimum": 2000,
          "maximum": 2100
        }
      },
      "description": "Use CheckoutRequest.card instead.",
      "deprecated": true,
      "required": ["expiry_year"]
    },
    "legacy_id": {
      "type": "string",
      "description": "Legacy identifier, kept for backwards compatibility.",
      "deprecated": true,
      "writeOnly": true
    },
    "mode": {
      "type": "string",
      "description": "Output mode.",
      "default": "compact",
      "examples": ["compact"],
      "enum": ["compact", "detailed"]
    },
    "rate": {
      "type": "number",
      "description": "Sampling rate.",
      "default": 0.5,
      "examples": [0.25, 1],
      "enum": [0.25, 0.5, 1]
    },
    "status": {
      "type": "integer",
      "description": "Status narrowed to a subset of the enum's declared values.",
      "default": 1,
      "enum": [1, 2]
    },
    "step": {
      "type": "integer",
      "description": "Step size in items.",
      "multipleOf": 5
    },
    "tags": {
      "type": "array",
      "items": {
        "type": "string",
        "enum": ["a", "b", "c"]
      },
      "description": "Tags: enum_string constrains each element; deprecated marks the array\n itself; min_items 0 exercises presence-based zero bounds.",
      "deprecated": true,
      "minItems": 0
    },
    "verbose": {
      "type": "boolean",
      "description": "Verbose flag.",
      "default": true
    },
    "weekday": {
      "type": "integer",
      "description": "Weekday index.",
      "default": 1,
      "examples": [1, 5],
      "enum": [0, 1, 2, 3, 4, 5, 6]
    }
  },
  "description": "KeywordShowcase exercises every new field option on happy paths.",
  "required": ["weekday", "rate", "mode", "verbose", "step", "legacy_id", "etag", "status", "legacy_card"]
}
```

---

## Generation-time errors

Invalid option combinations **abort generation with an error naming the
field** — never a silently wrong schema:

```text
field example.v1.M.mode: at most one enum_* variant may be set
field example.v1.M.count: the default_* variant for JSON type "string" does not match the field's JSON type "integer"
field example.v1.M.tags: default_* is only valid on singular scalar fields
field example.v1.M.step: multiple_of must be greater than 0
field example.v1.M.rate: exclusive_minimum requires minimum to be set
field example.v1.M.status: enum_int value 5 is not a declared value of enum example.v1.Status
```

The complete rule set:

- At most one variant per group (`enum_*`, `default_*`, `examples_*`).
- The variant must match the field's JSON type — string/bytes → `_string`;
  integer kinds and proto enums → `_int`; float/double → `_number`;
  bool → `default_bool`. For repeated/map fields, the element/value type
  governs `enum_*` and `multiple_of`.
- `default_*` / `examples_*`: singular scalar fields only.
- `enum_int` on a proto enum: values must be a subset of the declared values.
- `multiple_of`: numeric fields only, greater than 0.
- `exclusive_minimum`/`exclusive_maximum`: require their bound to be set.
- `enum_int`/`default_int` are `int64` and cannot express `uint64` values
  above 2⁶³−1.
