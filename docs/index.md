---
title: Home
nav_order: 1
---

# protoc-gen-go-jsonschema

A `protoc` plugin that generates Go code producing **JSON Schema (Draft 2020-12)**
representations of your protobuf messages at runtime.

```protobuf
message User {
  option (alis.open.options.v1.message).json_schema.generate = true;

  // Unique identifier for the user.
  string id = 1;
  // Email address of the user.
  string email = 2 [(alis.open.options.v1.field).json_schema = { format: "email" }];
}
```

```go
schema := (&userpb.User{}).JsonSchema() // *jsonschema.Schema, ready to use
```

```json
{
  "$ref": "#/$defs/users.v1.User",
  "type": "object",
  "$defs": {
    "users.v1.User": {
      "type": "object",
      "properties": {
        "id":    { "type": "string", "description": "Unique identifier for the user." },
        "email": { "type": "string", "format": "email", "description": "Email address of the user." }
      },
      "required": ["id", "email"]
    }
  }
}
```

## Why

Agents and MCP tools need a machine-readable contract for the values they accept
and produce. If those values are protobuf messages, this plugin turns the proto
definition — comments, constraints, and all — into a live `*jsonschema.Schema`
you can hand straight to an MCP server, a validator, or an LLM.

The schemas describe the JSON that Go's **`encoding/json`** produces for your
generated types (not `protojson`): snake_case property names, numeric enums,
PascalCase oneof wrappers. If your consumers call `json.Marshal`, the schema
matches what they see. See [Compatibility](compatibility.md) for the details.

## Highlights

- **One method per message** — `JsonSchema()` returns a complete, self-contained
  schema with referenced messages expanded in place: no `$ref`, no `$defs`, so
  MCP clients that cannot resolve references still get the full shape.
  Recursive and mutually referencing messages just work — they are the one
  case that keeps a `$defs` entry.
- **Docs from comments** — leading proto comments become `title`/`description`.
- **Rich constraint options** — formats, patterns, bounds, enum restrictions,
  defaults, examples, deprecation flags, and more, declared on the proto field.
- **Mutually exclusive field groups** — declare "exactly one of these fields"
  at the message level and the schema enforces it. Built for tool inputs where
  an LLM must pick one request shape.
- **MCP-ready** — schemas resolve with
  [`google/jsonschema-go`](https://github.com/google/jsonschema-go), the
  library the MCP Go SDK uses, so `mcp.AddTool` accepts them directly.

## Where to go next

| Page | What's there |
|---|---|
| [Getting started](getting-started.md) | Install the plugin, wire it into `protoc` or `buf`, generate your first schema |
| [Type mapping](type-mapping.md) | How every proto construct maps to JSON Schema |
| [Options reference](options.md) | Every file, message, and field option |
| [Oneof groups](oneof-groups.md) | Mutually exclusive field groups for LLM-facing requests |
| [Generated code](generated-code.md) | The API the plugin generates and how it behaves |
| [MCP integration](mcp.md) | Registering schemas with MCP tools |
| [Compatibility](compatibility.md) | Spec conformance, caveats, version requirements |
