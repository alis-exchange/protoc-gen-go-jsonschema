# protoc-gen-go-jsonschema

A Protocol Buffers compiler (protoc) plugin that generates Go code for creating JSON Schema (Draft 2020-12) representations of your proto messages at runtime.

**📖 Documentation: [alis-exchange.github.io/protoc-gen-go-jsonschema](https://alis-exchange.github.io/protoc-gen-go-jsonschema/)** — or browse the [docs/](docs/) folder.

> [!IMPORTANT]
> This plugin is designed to be used alongside [protoc-gen-go](https://pkg.go.dev/google.golang.org/protobuf/cmd/protoc-gen-go). It generates additional Go code that provides `JsonSchema()` methods for your proto messages.

## Features

- **JSON Schema Draft 2020-12** - Generates schemas following the latest JSON Schema specification
- **Runtime Schema Generation** - Each message gets a `JsonSchema()` method that returns a `*jsonschema.Schema`
- **Full Proto3 Support** - Handles all proto3 types including maps, repeated fields, oneofs, and enums
- **Google Types** - Proper handling of all Google types (google.protobuf._, google.type._, google.api._, google.iam._, etc.)
- **Inline by default** - Message-typed fields are expanded in place; `$defs`/`$ref` appear only for recursive messages, so MCP clients that cannot resolve references get clean schemas
- **Customizable** - Proto options allow fine-grained control over schema generation and validation constraints

## Installation

### Using go install

All dependencies (including the alis options module `go.alis.build/common/alis/open/options`) resolve from the public Go module proxy — no special configuration is needed:

```shell
go install github.com/alis-exchange/protoc-gen-go-jsonschema/cmd/protoc-gen-go-jsonschema@latest
```

### Download Pre-built Binary

Alternatively, download a pre-built binary from the [releases page](https://github.com/alis-exchange/protoc-gen-go-jsonschema/releases).

## Usage

### 1. Enable Schema Generation in Your Proto File

Add the JSON Schema option to enable generation:

```protobuf
syntax = "proto3";

package example.v1;

import "alis/open/options/v1/options.proto";

option go_package = "example.com/api/v1;examplev1";

// Enable JSON Schema generation for all messages in this file
option (alis.open.options.v1.file).json_schema.generate = true;

message User {
  string id = 1;
  string name = 2;
  string email = 3;
  repeated string tags = 4;
}
```

### 2. Run protoc with the Plugin

```shell
protoc --go_out=. --go_opt=paths=source_relative --go-jsonschema_out=. --go-jsonschema_opt=paths=source_relative path/to/your.proto
```

### 3. Use the Generated Code

The plugin generates a `*_jsonschema.pb.go` file with `JsonSchema()` methods:

```go
package main

import (
    "encoding/json"
    "fmt"

    examplev1 "example.com/api/v1"
)

func main() {
    // Get the JSON Schema for the User message
    user := &examplev1.User{}
    schema := user.JsonSchema()

    // Marshal to JSON
    jsonBytes, _ := json.MarshalIndent(schema, "", "  ")
    fmt.Println(string(jsonBytes))
}
```

This produces JSON Schema output like:

```json
{
  "type": "object",
  "properties": {
    "id": { "type": "string" },
    "name": { "type": "string" },
    "email": { "type": "string" },
    "tags": {
      "type": "array",
      "items": { "type": "string" }
    }
  },
  "required": ["id", "name", "email", "tags"]
}
```

## Proto Options

### File-Level Options

Enable schema generation for all messages in a file:

```protobuf
option (alis.open.options.v1.file).json_schema.generate = true;
```

### Message-Level Options

Override file-level settings for specific messages:

```protobuf
message InternalMessage {
  option (alis.open.options.v1.message).json_schema.generate = false;
  // This message will not generate a schema
}
```

### Field-Level Options

Customize individual field schemas:

```protobuf
message User {
  string email = 1 [(alis.open.options.v1.field).json_schema = {
    format: "email"
    title: "Email Address"
    description: "User's primary email"
  }];

  int32 age = 2 [(alis.open.options.v1.field).json_schema = {
    minimum: 0
    maximum: 150
  }];

  string phone = 3 [(alis.open.options.v1.field).json_schema = {
    pattern: "^\\+[1-9]\\d{1,14}$"
  }];

  // Exclude a field from the schema
  string internal_notes = 4 [(alis.open.options.v1.field).json_schema = {
    ignore: true
  }];
}
```

#### Available Field Options

| Option               | Type   | Description                                      |
| -------------------- | ------ | ------------------------------------------------ |
| `ignore`             | bool   | Exclude field from schema                        |
| `title`              | string | Schema title                                     |
| `description`        | string | Schema description                               |
| `format`             | string | JSON Schema format (email, uri, date-time, etc.) |
| `pattern`            | string | Regex pattern for string validation              |
| `minimum`            | double | Minimum value for numbers                        |
| `maximum`            | double | Maximum value for numbers                        |
| `exclusive_minimum`  | bool   | Make minimum exclusive (see note below)          |
| `exclusive_maximum`  | bool   | Make maximum exclusive (see note below)          |
| `min_length`         | uint64 | Minimum string length                            |
| `max_length`         | uint64 | Maximum string length                            |
| `min_items`          | uint64 | Minimum array length                             |
| `max_items`          | uint64 | Maximum array length                             |
| `unique_items`       | bool   | Require unique array items                       |
| `min_properties`     | uint64 | Minimum object properties                        |
| `max_properties`     | uint64 | Maximum object properties                        |
| `content_encoding`   | string | Content encoding (e.g., "base64")                |
| `content_media_type` | string | Content media type                               |

> [!NOTE]
> **`exclusive_minimum` / `exclusive_maximum` semantics (JSON Schema draft 2020-12)**
>
> Setting `exclusive_minimum: true` alongside `minimum: N` emits `ExclusiveMinimum: N` on the generated schema (and _does not_ emit `Minimum`). Under draft 2020-12 the exclusive variants are standalone numeric values that replace — rather than modify — the inclusive bound. The same applies to `exclusive_maximum`/`maximum`. If you're coming from an older draft where these were boolean modifiers, note that you only get one or the other per bound.

## Compatibility

The generated code targets [`github.com/google/jsonschema-go`](https://pkg.go.dev/github.com/google/jsonschema-go) **v0.4.x**. Downstream consumers should pin a compatible version in their `go.mod`. If upstream schema-struct field types change in a later major, the plugin will need to be updated — please file an issue if you hit a compile error against a newer `jsonschema-go`.

## Type Mapping

### Field Names

Generated schemas use **proto field names** (snake_case) as property keys, not JSON names (camelCase). This is designed for use with `json.Marshal` rather than `protojson.Marshal`:

```protobuf
message User {
  string first_name = 1;  // Schema property: "first_name" (not "firstName")
}
```

### Oneof Fields

User-message oneofs use **nested PascalCase wrappers** to match `encoding/json` output from protoc-gen-go (oneof interface fields and wrapper structs have no `json` tags, so Go field names are used):

```json
{
  "Identifier": { "Email": "user@example.com" },
  "PaymentMethod": { "CreditCard": "4111111111111111" },
  "ContactPreference": null
}
```

Each wrapper accepts either `null` (the oneof is unset — `encoding/json` always emits the key, with value `null`) or an object holding exactly one variant. This differs from flat `protojson`-style oneofs where variant fields appear as snake_case root properties. Google types with oneofs get the same wrappers: they have no custom `json.Marshaler`, so `encoding/json` treats them like any other generated message.

> [!NOTE]
> Field-level comments and options on message-type fields (including oneof message variants) decorate the referenced message's schema: on the inline copy a field comment replaces the message's own title and description; for recursive messages they ride as `$ref` siblings.

### Type Conversions

| Proto Type                                         | JSON Schema Type | Notes                       |
| -------------------------------------------------- | ---------------- | --------------------------- |
| `string`                                           | `string`         |                             |
| `bool`                                             | `boolean`        |                             |
| `int32`, `sint32`, `uint32`, `fixed32`, `sfixed32` | `integer`        |                             |
| `int64`, `sint64`, `uint64`, `fixed64`, `sfixed64` | `integer`        |                             |
| `float`, `double`                                  | `number`         |                             |
| `bytes`                                            | `string`         | contentEncoding: "base64"   |
| `enum`                                             | `integer`        | With `enum` constraint      |
| `message`                                          | `object`         | Inlined; `$ref` only for recursive messages |
| `repeated T`                                       | `array`          | With `items` schema         |
| `map<K, V>`                                        | `object`         | With `additionalProperties` |

## Google Types

All Google types (`google.*` packages including `google.protobuf.*`, `google.type.*`, `google.api.*`, `google.iam.*`, etc.) are handled like normal messages - they generate schemas based on their actual proto field structure, not the special JSON encoding used by `protojson`. This is designed for use with standard `json.Marshal`.

The exceptions are `google.protobuf.Struct`, `Value` and `ListValue`: their Go types implement `json.Marshaler` with plain-JSON semantics, so they map to free-form schemas — `{"type": "object"}`, `{"type": "array"}`, and for `Value` an explicit `type` list of every JSON type.

Since Google types are imported types, the plugin generates **standalone functions** (not methods) with file-prefixed names to ensure uniqueness (the prefix is the proto file's base name, with characters that cannot appear in a Go identifier replaced by `_`):

```go
// Generated for Google types (standalone functions, not methods)
func user_google_protobuf_Timestamp_JsonSchema() *jsonschema.Schema { ... }
func common_google_iam_admin_v1_ServiceAccountKey_JsonSchema() *jsonschema.Schema { ... }
```

## Dependencies

This plugin generates code that uses:

- [`github.com/google/jsonschema-go/jsonschema`](https://pkg.go.dev/github.com/google/jsonschema-go/jsonschema) - JSON Schema types

Add this to your project:

```shell
go get github.com/google/jsonschema-go
```

## Testing

Tests live in the `plugin_test/` package and run with plain `go test`. Tests
that need `protoc`, `protoc-gen-go`, or network access skip themselves when the
tool is missing (or with `-short`):

```shell
go test ./...

# Update golden files (goldens exist for every proto under testdata/protos)
go test ./plugin_test/... -update
```

## Contributing

Contributions are welcome! Please feel free to submit issues and pull requests.

## License

See [LICENSE](LICENSE) for details.
