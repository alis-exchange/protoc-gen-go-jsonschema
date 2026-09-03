---
title: Compatibility
nav_order: 8
---

# Compatibility

## encoding/json, not protojson

The schemas describe the JSON that Go's **`encoding/json`** produces for
protoc-gen-go types. That is deliberate: agents and MCP tools overwhelmingly
call `json.Marshal` on the generated structs. The visible consequences:

| | `encoding/json` (this plugin) | `protojson` |
|---|---|---|
| Property names | snake_case (`first_name`) | camelCase (`firstName`) |
| Enums | numbers (`1`) | names (`"STATUS_ACTIVE"`) |
| Oneofs | nested PascalCase wrapper (`{"Identifier": {"Email": ...}}`) | flat (`{"email": ...}`) |
| 64-bit ints | numbers | strings |

If your consumers use `protojson.Marshal`, these schemas will not match their
output.

## JSON Schema Draft 2020-12

Generated schemas target Draft 2020-12. Messages without recursion inline
completely — no `$ref`, no `$defs` — because MCP clients and SDKs do not
reliably resolve references (see [MCP integration](mcp.md)). Only recursive
messages rely on Draft 2020-12 behaviors:

- **`$defs` + `$ref`** for messages on a reference cycle, always beneath a
  literal `type: "object"` root.
- **Sibling keywords next to `$ref`** — field descriptions, annotations, and
  constraints on a field referencing a recursive message ride the `$ref`
  directly. Draft-07-era validators ignore siblings of `$ref`; they still
  validate the structure correctly but drop that metadata.

Schemas resolve and validate with
[`google/jsonschema-go`](https://github.com/google/jsonschema-go), which the
MCP Go SDK uses.

## Required vs omitempty
{: #required-vs-omitempty }

`required` states the **proto3 contract**: singular fields — scalars and
messages alike — are required unless declared `optional`. Meanwhile
protoc-gen-go tags every field `omitempty`, so `json.Marshal` omits
zero-valued scalars and nil message fields. Two practical consequences:

1. A marshaled value whose required scalar happens to be zero (`0`, `""`,
   `false`) fails validation against its own schema. The schema describes what
   a producer *should* supply, not every byte pattern `json.Marshal` can emit.
2. For "at most one / exactly one of these fields" semantics, use
   [oneof groups](oneof-groups.md) over **message-typed** fields — message
   presence (set vs nil) survives marshaling faithfully.

Ways to take a field out of `required`: mark it `optional`, put it in a
declared oneof group, or make it repeated/map.

One case demands it: a singular, non-`optional` field whose type is the
message itself (or otherwise closes a reference cycle) makes the schema
unsatisfiable — no finite document can supply the field at every depth. Mark
such fields `optional`.

## Version requirements

| Feature | Plugin | Options module (`go.alis.build/common/alis/open/options`) |
|---|---|---|
| Core generation, comments, classic constraints | any | any |
| `$ref` sibling metadata on message fields | v0.2.0+ | any |
| Inline-by-default schemas (`$defs` only for cycles), free-form Struct/Value/ListValue | v0.3.0+ | any |
| Typed variants (`enum_*`, `default_*`, `examples_*`), annotations, `multiple_of` | v0.2.0+ | v1.7.0+ |
| Message-level `oneof` groups | v0.2.0+ | v1.8.0+ |
| Presence-based reads (`minimum: 0` expressible), `optional generate` | v0.2.0+ | v1.8.0+ |

Older plugin binaries **silently ignore** options they don't know. If a proto
uses v0.2.0 options, make sure generation runs with a v0.2.0+ plugin —
otherwise the constraints quietly vanish from the schema.

## Upgrading to v0.3.0

The schema **shape** changes for every message. The generated Go API changes
only at the edges (last two bullets).

- **Roots are real objects.** `JsonSchema()` returns `type: "object"` with
  `properties` at the root instead of a `$ref` wrapper. Code that dug the
  definition out of `schema.Defs[...]` should read the root directly.
- **No `$defs`/`$ref` for non-recursive messages.** Message-typed fields are
  expanded in place. Only messages on a reference cycle keep a `$defs` entry,
  and `$ref` appears only where the recursion happens.
- **Field metadata replaces on inline copies.** A comment or
  `title`/`description` option on a message-typed field replaces the
  referenced message's own title *and* description in that spot (previously
  both were present as `$ref` target plus sibling).
- **Google types with oneofs use PascalCase wrappers** like user messages
  (e.g. `google.iam.admin.v1.LintPolicyRequest` → `LintObject`). The old
  flat shape only matched types with a custom marshaler, which are now
  free-form.
- **`google.protobuf.Struct`, `Value`, `ListValue` are free-form**
  (`{"type": "object"}`, `{"type": "array"}`, and for `Value` an explicit `type` list of every JSON type) — matching what `encoding/json`
  actually emits for them. Their standalone `*_google_protobuf_{Struct,Value,ListValue}_*`
  functions are **no longer generated**; code that called them must be
  removed.
- **Recursive messages gain an exported `_JsonSchema_build(defs, register)`
  helper** used by the generated file itself. Existing `_WithDefs` names and
  signatures are unchanged, and packages generated by v0.2.x and v0.3.x
  compose (a caller never inspects what `_WithDefs` returns).

## Upgrading to v0.2.0

Behavior changes to review when regenerating with v0.2.0:

- **Message-field metadata now emits.** Comments and options on message-typed
  fields, previously dropped, appear as `$ref` siblings. Additive for
  annotations — but a *constraint* option (e.g. `min_properties`) that was
  silently ignored before now validates. Documents that passed may start
  failing where such dormant options exist.
- **`minimum: 0` and friends now emit** (presence-based reads), and
  `exclusive_minimum` without a bound is a generation-time error instead of
  silently emitting 0.
- **Empty `title`/`description` keywords are no longer written** into the
  generated Go. No schema-JSON change (they never serialized).
- **Unsupported field kinds fail generation** instead of emitting `"type": ""`.
- Generated function names and signatures are unchanged — no code changes for
  consumers, just regeneration.
