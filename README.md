# protoc-gen-go-jsonschema

A Protocol Buffers compiler (protoc) plugin that generates Go code for creating JSON Schema (Draft 2020-12) representations of your proto messages at runtime.

> [!IMPORTANT]
> This plugin is designed to be used alongside [protoc-gen-go](https://pkg.go.dev/google.golang.org/protobuf/cmd/protoc-gen-go). It generates additional Go code that provides `JsonSchema()` methods for your proto messages.

## Features

- **JSON Schema Draft 2020-12** - Generates schemas following the latest JSON Schema specification
- **Runtime Schema Generation** - Each message gets a `JsonSchema()` method that returns a `*jsonschema.Schema`
- **Full Proto3 Support** - Handles all proto3 types including maps, repeated fields, oneofs, and enums
- **Well-Known Types** - Proper handling of Google's WKTs (Timestamp, Duration, Struct, Any, etc.)
- **Cross-References** - Messages reference each other using JSON Schema `$defs` and `$ref`
- **Customizable** - Proto options allow fine-grained control over schema generation and validation constraints

## Installation

```shell
go install github.com/alis-exchange/protoc-gen-go-jsonschema/cmd/protoc-gen-go-jsonschema@latest
```

Or download a pre-built binary from the [releases page](https://github.com/alis-exchange/protoc-gen-go-jsonschema/releases).

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

| Option | Type | Description |
|--------|------|-------------|
| `ignore` | bool | Exclude field from schema |
| `title` | string | Schema title |
| `description` | string | Schema description |
| `format` | string | JSON Schema format (email, uri, date-time, etc.) |
| `pattern` | string | Regex pattern for string validation |
| `minimum` | double | Minimum value for numbers |
| `maximum` | double | Maximum value for numbers |
| `exclusive_minimum` | bool | Make minimum exclusive |
| `exclusive_maximum` | bool | Make maximum exclusive |
| `min_length` | uint64 | Minimum string length |
| `max_length` | uint64 | Maximum string length |
| `min_items` | uint64 | Minimum array length |
| `max_items` | uint64 | Maximum array length |
| `unique_items` | bool | Require unique array items |
| `min_properties` | uint64 | Minimum object properties |
| `max_properties` | uint64 | Maximum object properties |
| `content_encoding` | string | Content encoding (e.g., "base64") |
| `content_media_type` | string | Content media type |

## Type Mapping

| Proto Type | JSON Schema Type | Notes |
|------------|------------------|-------|
| `string` | `string` | |
| `bool` | `boolean` | |
| `int32`, `sint32`, `uint32`, `fixed32`, `sfixed32` | `integer` | |
| `int64`, `sint64`, `uint64`, `fixed64`, `sfixed64` | `string` | Pattern: `^-?[0-9]+$` (JS precision) |
| `float`, `double` | `number` | |
| `bytes` | `string` | contentEncoding: "base64" |
| `enum` | `string` | With `enum` constraint |
| `message` | `object` | Or `$ref` to definition |
| `repeated T` | `array` | With `items` schema |
| `map<K, V>` | `object` | With `additionalProperties` |

## Well-Known Types

Google's well-known types are handled with appropriate JSON representations:

| WKT | JSON Schema |
|-----|-------------|
| `google.protobuf.Timestamp` | `string` with `format: "date-time"` |
| `google.protobuf.Duration` | `string` with duration pattern |
| `google.protobuf.Struct` | `object` (arbitrary JSON) |
| `google.protobuf.Value` | Any JSON value |
| `google.protobuf.Any` | `object` with `@type` field |
| `google.protobuf.FieldMask` | `string` (comma-separated paths) |
| `google.protobuf.Empty` | `object` (empty) |
| `google.protobuf.*Value` | Corresponding primitive type |

## Dependencies

This plugin generates code that uses:
- [`github.com/google/jsonschema-go/jsonschema`](https://pkg.go.dev/github.com/google/jsonschema-go/jsonschema) - JSON Schema types

Add this to your project:

```shell
go get github.com/google/jsonschema-go
```

## Contributing

Contributions are welcome! Please feel free to submit issues and pull requests.

## License

See [LICENSE](LICENSE) for details.
