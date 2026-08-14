---
title: MCP integration
nav_order: 7
---

# MCP integration

The primary consumer of these schemas is an MCP server describing its tools.
The generated schemas resolve with
[`google/jsonschema-go`](https://github.com/google/jsonschema-go) — the same
library the [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk)
uses internally — so they plug into `mcp.AddTool` directly.

## Registering a tool

```go
import (
    "github.com/modelcontextprotocol/go-sdk/mcp"
    demov1 "example.com/demo/v1"
)

server := mcp.NewServer(&mcp.Implementation{Name: "demo", Version: "1.0.0"}, nil)

tool := &mcp.Tool{
    Name:         "create_task",
    Description:  "Create a task",
    InputSchema:  (&demov1.CreateTaskRequest{}).JsonSchema(),
    OutputSchema: (&demov1.Task{}).JsonSchema(),
}

mcp.AddTool(server, tool, func(ctx context.Context, req *mcp.CallToolRequest, input *demov1.CreateTaskRequest) (*mcp.CallToolResult, *demov1.Task, error) {
    // input has already been validated against the schema by the SDK.
    ...
})
```

Two things happen with the schema:

1. **Wire schema (`tools/list`)** — clients receive the schema exactly as
   `JsonSchema()` built it: a `$ref` root plus `$defs`. The comments,
   constraints, defaults, and exactly-one groups from your proto are what the
   LLM sees when deciding how to call the tool.
2. **Server-side validation (`tools/call`)** — the SDK resolves the schema at
   `AddTool` time and validates incoming arguments against it before your
   handler runs. Invalid inputs — a missing required field, a value outside an
   `enum_int` subset, two members of an exactly-one group — are rejected with
   a schema error, not passed to your code.

**Set `OutputSchema` explicitly.** The SDK's reflection-based schema inference
(`jsonschema.ForType`) cannot handle recursive proto types; `JsonSchema()` can.

## Guiding the model

Everything the model needs rides in the schema:

- **Comments become descriptions** — write proto comments as instructions to
  the caller ("Use only when the user asks for a due date.").
- **[Oneof groups](oneof-groups.md)** make "pick exactly one of these
  request shapes" a hard constraint instead of prose.
- **`default_*` / `examples_*`** give the model anchors for well-formed values.
- **`enum_*`** closes open-ended strings into fixed vocabularies.
- **`read_only`** marks fields the model should never populate;
  **`deprecated`** steers it away from legacy fields.

## Validating without MCP

The same schemas work standalone:

```go
schema := (&demov1.CreateTaskRequest{}).JsonSchema()
resolved, err := schema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
if err != nil { ... }

var doc map[string]any
_ = json.Unmarshal(raw, &doc)
if err := resolved.Validate(doc); err != nil {
    // reject the input with a precise schema error
}
```

Validate the **root schema** returned by `JsonSchema()` — a bare definition
plucked out of `$defs` has no resolution context for its `$ref`s.
