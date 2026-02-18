# protoc-gen-go-jsonschema

A Protocol Buffers compiler (protoc) plugin that generates Go code for creating JSON Schema (Draft 2020-12) representations of your proto messages at runtime.

> [!IMPORTANT]
> This plugin is designed to be used alongside [protoc-gen-go](https://pkg.go.dev/google.golang.org/protobuf/cmd/protoc-gen-go). It generates additional Go code that provides `JsonSchema()` methods for your proto messages.

## Features

- **JSON Schema Draft 2020-12** - Generates schemas following the latest JSON Schema specification
- **Runtime Schema Generation** - Each message gets a `JsonSchema()` method that returns a `*jsonschema.Schema`
- **Full Proto3 Support** - Handles all proto3 types including maps, repeated fields, oneofs, and enums
- **Google Types** - Proper handling of all Google types (google.protobuf._, google.type._, google.api._, google.iam._, etc.)
- **Cross-References** - Messages reference each other using JSON Schema `$defs` and `$ref`
- **Customizable** - Proto options allow fine-grained control over schema generation and validation constraints

## Installation

### Using go install

This plugin depends on a private module (`open.alis.services/protobuf`), so you'll need to configure Go's module proxy settings:

```shell
GOPROXY=https://europe-west1-go.pkg.dev/alis-org-777777/openprotos-go,https://proxy.golang.org,direct \
GONOPROXY=github.com/alis-exchange/protoc-gen-go-jsonschema \
GONOSUMDB=open.alis.services/protobuf \
go install github.com/alis-exchange/protoc-gen-go-jsonschema/cmd/protoc-gen-go-jsonschema@latest
```

**Environment Variables Explained:**

- `GOPROXY`: Configures the module proxy chain, including the Artifact Registry Repository for `open.alis.services/protobuf`
- `GONOPROXY`: Excludes this public GitHub module from the Artifact Registry Repository
- `GONOSUMDB`: Disables checksum verification for the Artifact Registry Repository

### Download Pre-built Binary

Alternatively, download a pre-built binary from the [releases page](https://github.com/alis-exchange/protoc-gen-go-jsonschema/releases).

## Usage

### 1. Enable Schema Generation in Your Proto File

Add the JSON Schema option to enable generation:

```protobuf
syntax = "proto3";

package example.v1;

import "alis/open/options/v1/options.proto";

option go_package = "github.com/example/api/v1;examplev1";

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

    examplev1 "github.com/example/api/v1"
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
| `exclusive_minimum`  | bool   | Make minimum exclusive                           |
| `exclusive_maximum`  | bool   | Make maximum exclusive                           |
| `min_length`         | uint64 | Minimum string length                            |
| `max_length`         | uint64 | Maximum string length                            |
| `min_items`          | uint64 | Minimum array length                             |
| `max_items`          | uint64 | Maximum array length                             |
| `unique_items`       | bool   | Require unique array items                       |
| `min_properties`     | uint64 | Minimum object properties                        |
| `max_properties`     | uint64 | Maximum object properties                        |
| `content_encoding`   | string | Content encoding (e.g., "base64")                |
| `content_media_type` | string | Content media type                               |

## Type Mapping

### Field Names

Generated schemas use **proto field names** (snake_case) as property keys, not JSON names (camelCase). This is designed for use with `json.Marshal` rather than `protojson.Marshal`:

```protobuf
message User {
  string first_name = 1;  // Schema property: "first_name" (not "firstName")
}
```

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
| `message`                                          | `object`         | Or `$ref` to definition     |
| `repeated T`                                       | `array`          | With `items` schema         |
| `map<K, V>`                                        | `object`         | With `additionalProperties` |

## Google Types

All Google types (`google.*` packages including `google.protobuf.*`, `google.type.*`, `google.api.*`, `google.iam.*`, etc.) are handled like normal messages - they generate schemas based on their actual proto field structure, not the special JSON encoding used by `protojson`. This is designed for use with standard `json.Marshal`.

Since Google types are imported types, the plugin generates **standalone functions** (not methods) with file-prefixed names to ensure uniqueness:

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

Tests live in the `plugin_test/` package and require the `plugintest` build tag:

```shell
go test -tags=plugintest ./plugin_test/...

# Update golden files
go test -tags=plugintest ./plugin_test/... -update
```

## Contributing

Contributions are welcome! Please feel free to submit issues and pull requests.

## License

See [LICENSE](LICENSE) for details.
