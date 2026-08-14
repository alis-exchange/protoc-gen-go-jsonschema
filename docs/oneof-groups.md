---
title: Oneof groups
nav_order: 5
---

# Oneof groups
{: .no_toc }

**Requires protoc-gen-go-jsonschema v0.2.0+.**

Declare mutually exclusive field groups at the message level, and the
generated schema enforces them. This is the tool for LLM-facing request
messages where the model must pick **exactly one** request shape — the same
pattern as `buf.validate.message.oneof`, carried into the JSON Schema.

## The motivating shape

```protobuf
message CheckoutRequest {
  option (alis.open.options.v1.message).json_schema.oneof = {
    fields: ["card", "bank_transfer", "mobile_money"],
    required: true   // exactly one must be set
  };
  option (alis.open.options.v1.message).json_schema.oneof = {
    fields: ["promo_code", "gift_card_code"]   // at most one may be set
  };

  // Pay with a saved card.
  Card card = 1;
  // Pay by bank transfer.
  BankTransfer bank_transfer = 2;
  // Pay with mobile money.
  MobileMoney mobile_money = 3;

  // Optional promotional code.
  string promo_code = 4;
  // Optional gift card code.
  string gift_card_code = 5;
}
```

The generated schema keeps each field's normal property (with its `$ref` and
description) and adds root-level constraints:

```json
{
  "allOf": [
    { "oneOf": [
        { "required": ["card"] },
        { "required": ["bank_transfer"] },
        { "required": ["mobile_money"] }
    ]},
    { "oneOf": [
        { "required": ["promo_code"] },
        { "required": ["gift_card_code"] },
        { "not": { "anyOf": [ { "required": ["promo_code"] }, { "required": ["gift_card_code"] } ] } }
    ]}
  ]
}
```

- `required: true` → exactly one member (no "none set" branch).
- `required` unset/false → at most one member (a "none set" branch joins the
  alternatives).
- One group emits `oneOf` directly; several groups nest under `allOf`.

## Semantics

**Members leave the plain `required` array.** A field named in a group is
conditionally required by the group's constraint, never by `required`. This is
also the idiomatic way to make a set of singular message fields optional-but-
exclusive without marking each one `optional`.

**Members may be ordinary fields or real proto `oneof` members.** Ordinary
singular fields (a "virtual oneof") use plain `{"required": ["field_name"]}`
presence. Members of a real proto `oneof` block are matched through their
PascalCase wrapper (which is always present under `encoding/json`, `null` when
unset):

```json
{
  "required": ["Selector"],
  "properties": { "Selector": { "type": "object", "required": ["ById"] } }
}
```

**Narrowing is allowed.** A group may deliberately cover only a subset of a
proto `oneof`'s members — the schema then narrows the contract, the same
philosophy as `enum_int` narrowing a proto enum. With `required: true`, a
legal proto value that sets an uncovered variant fails schema validation; that
is the point.

**Prefer message-typed members.** Presence detection relies on the key being
present in the JSON. `json.Marshal` omits unset message fields (accurate) but
also omits zero-valued scalars (`omitempty`), so a scalar member set to its
zero value reads as "not set". Groups over message-typed fields — like the
payment shapes above — behave exactly as declared.

## Validation

Declaring an invalid group aborts generation with an error naming the message:

- fewer than two members,
- a member name that doesn't exist on the message,
- repeated or map members,
- members excluded by `ignore`,
- a field appearing in more than one group.

## Why not just use a proto `oneof`?

Use both — they solve different halves:

| | proto `oneof` | `json_schema.oneof` |
|---|---|---|
| Wire/Go representation | one shared slot, wrapper types | ordinary fields |
| JSON shape (`encoding/json`) | nested PascalCase wrapper | flat snake_case properties |
| Can require exactly-one | no | yes (`required: true`) |
| Adoptable without breaking Go callers | no (changes generated Go API) | yes (schema-only) |

For LLM-facing inputs, flat snake_case properties plus an exactly-one
constraint is usually the friendlier contract; a declared group gives you that
without restructuring the proto. If you already have a proto `oneof`, a
declared group over its members adds the missing "exactly one" requirement.
