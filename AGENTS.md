# AI Agent Guide for protoc-gen-go-jsonschema

This document provides comprehensive information for AI agents and LLMs working with this codebase.

## Project Overview

**protoc-gen-go-jsonschema** is a Protocol Buffers compiler plugin that generates Go code for creating JSON Schema (Draft 2020-12) representations of proto messages at runtime.

- **Repository**: `github.com/alis-exchange/protoc-gen-go-jsonschema`
- **Language**: Go
- **Purpose**: Generate `JsonSchema()` methods for proto messages
- **Output**: JSON Schema Draft 2020-12 compliant schemas
- **Key Dependency**: `github.com/google/jsonschema-go/jsonschema`

### What This Plugin Does

For each targeted proto message, the plugin generates:

1. **`JsonSchema()` method** - Public API that returns a complete `*jsonschema.Schema`
2. **`<Message>_JsonSchema_WithDefs()` function** - Internal helper for recursive schema building with shared definitions

```go
// Generated code example
func (x *User) JsonSchema() *jsonschema.Schema {
    defs := make(map[string]*jsonschema.Schema)
    _ = User_JsonSchema_WithDefs(defs)
    root := defs["package.User"]
    root.Definitions = defs
    return root
}
```

---

## Project Structure

```
protoc-gen-go-jsonschema/
├── cmd/
│   └── protoc-gen-go-jsonschema/
│       └── main.go              # Plugin entry point, handles CLI flags
├── plugin/
│   ├── plugin.go                # Generate() function - main entry point
│   ├── functions.go             # Core schema generation logic (~1200 lines)
│   ├── suite_test.go            # Base test suite with shared fixtures
│   ├── integration_test.go      # End-to-end integration tests
│   ├── plugin_test.go           # Generator and plugin tests
│   ├── functions_test.go        # Unit tests for helper functions
│   └── testutil_test.go         # Test utility functions
├── testdata/
│   ├── protos/                  # Sample proto files for testing
│   │   └── users/v1/user.proto
│   ├── descriptors/             # Generated FileDescriptorSet files
│   │   └── user.pb
│   └── golden/                  # Expected output for golden file tests
│       └── user_jsonschema.pb.go.golden
├── build.sh                     # Cross-platform build script
├── install.sh                   # Installation script
├── go.mod
├── go.sum
├── LICENSE
└── README.md
```

---

## Core Concepts

### Generation Flow

```
protoc invokes plugin
         │
         ▼
   plugin.Generate()
         │
         ▼
 Generator.generateFile()
         │
         ├─── Generator.getMessages()  ──► Collects target messages
         │
         ▼
 MessageSchemaGenerator.generateMessageJSONSchema()
         │
         ├─── For each field:
         │         │
         │         ▼
         │    generateFieldJSONSchema()
         │         │
         │    ┌────┴────┬──────────┐
         │    ▼         ▼          ▼
         │  IsList?   IsMap?    Scalar?
         │    │         │          │
         │    ▼         ▼          ▼
         │  getArray   getMap    getScalar
         │  Config     Config    Config
         │    │         │          │
         │    └────┬────┴──────────┘
         │         ▼
         │    emitSchemaField()
         │
         ▼
   Generated *_jsonschema.pb.go file
```

### Key Types

#### `Generator` (plugin/functions.go)

Stateless coordinator for file-level generation:
- `generateFile()` - Creates output file, iterates messages
- `getMessages()` - Recursively collects messages to generate (respects options)
- `escapeGoString()` - Escapes strings for Go source code
- `getTitleAndDescription()` - Extracts metadata from proto comments

#### `MessageSchemaGenerator` (plugin/functions.go)

Stateful per-message schema builder:
- `gr` - Reference to parent Generator
- `gen` - Output file writer (`*protogen.GeneratedFile`)
- `visited` - Map tracking processed messages (prevents infinite recursion)

Key methods:
- `generateMessageJSONSchema()` - Generates complete schema for a message
- `generateFieldJSONSchema()` - Routes to appropriate config builder
- `emitSchemaField()` - Generates Go code for a field's schema
- `getArraySchemaConfig()` - Creates config for repeated fields
- `getMapSchemaConfig()` - Creates config for map fields
- `getScalarSchemaConfig()` - Creates config for scalar/message fields
- `getMessageSchemaConfig()` - Handles WKTs and message references
- `getKindTypeName()` - Maps proto kinds to JSON Schema types

#### `schemaFieldConfig` (plugin/functions.go)

Intermediate representation for field schemas:

```go
type schemaFieldConfig struct {
    fieldName            string  // JSON field name
    title                string  // Schema title
    description          string  // Schema description
    typeName             string  // JSON Schema type ("string", "object", etc.)
    format               string  // Format annotation ("date-time", "email", etc.)
    pattern              string  // Regex pattern
    propertyNamesPattern string  // Pattern for map keys
    enumValues           []string // Allowed enum values
    isBytes              bool    // Requires base64 contentEncoding
    messageRef           string  // Reference function call for messages
    nested               *schemaFieldConfig // For array items / map values
}
```

---

## Type Mapping Reference

### Scalar Types

| Proto Type | JSON Schema Type | Additional Constraints |
|------------|------------------|----------------------|
| `string` | `"string"` | — |
| `bool` | `"boolean"` | — |
| `int32`, `sint32`, `sfixed32` | `"integer"` | — |
| `uint32`, `fixed32` | `"integer"` | — |
| `int64`, `sint64`, `sfixed64` | `"string"` | `pattern: "^-?[0-9]+$"` |
| `uint64`, `fixed64` | `"string"` | `pattern: "^-?[0-9]+$"` |
| `float` | `"number"` | — |
| `double` | `"number"` | — |
| `bytes` | `"string"` | `contentEncoding: "base64"` |
| `enum` | `"string"` | `enum: ["VALUE1", "VALUE2", ...]` |

**Why 64-bit integers are strings**: JavaScript's `Number.MAX_SAFE_INTEGER` is 2^53-1. Values outside this range lose precision, so proto3 JSON encoding represents them as strings.

### Complex Types

| Proto Type | JSON Schema Type | Structure |
|------------|------------------|-----------|
| `message` | `"object"` | Properties for each field, or `$ref` |
| `repeated T` | `"array"` | `items` contains element schema |
| `map<K, V>` | `"object"` | `additionalProperties` contains value schema |
| `oneof` | — | `oneOf` constraint with `required` for each option |

### Map Key Handling

Map keys are always strings in JSON. Non-string proto keys use `propertyNames` validation:

| Proto Key Type | propertyNames Pattern |
|----------------|----------------------|
| `string` | (none) |
| `int32`, `int64`, etc. | `"^-?[0-9]+$"` |
| `bool` | `"^(true\|false)$"` |

---

## Well-Known Types (WKTs)

Google's well-known types have special JSON representations:

| WKT | JSON Schema Type | Format/Pattern | Description |
|-----|------------------|----------------|-------------|
| `google.protobuf.Timestamp` | `"string"` | `format: "date-time"` | RFC 3339 timestamp |
| `google.protobuf.Duration` | `"string"` | `pattern: "^([0-9]+\.?[0-9]*\|.[0-9]+)s$"` | Duration like "1.5s" |
| `google.protobuf.Struct` | `"object"` | — | Arbitrary JSON object |
| `google.protobuf.Value` | (any) | — | Any JSON value |
| `google.protobuf.ListValue` | `"array"` | — | JSON array |
| `google.protobuf.Any` | `"object"` | — | Must include @type |
| `google.protobuf.FieldMask` | `"string"` | — | Comma-separated paths |
| `google.protobuf.Empty` | `"object"` | — | Empty object |
| `google.protobuf.BoolValue` | `"boolean"` | — | Nullable bool |
| `google.protobuf.StringValue` | `"string"` | — | Nullable string |
| `google.protobuf.Int32Value` | `"integer"` | — | Nullable int32 |
| `google.protobuf.Int64Value` | `"string"` | `pattern: "^-?[0-9]+$"` | Nullable int64 |
| `google.protobuf.UInt32Value` | `"integer"` | — | Nullable uint32 |
| `google.protobuf.UInt64Value` | `"string"` | `pattern: "^-?[0-9]+$"` | Nullable uint64 |

WKTs are handled inline in `getMessageSchemaConfig()` - they do NOT generate separate `$ref` definitions.

---

## Options System

The plugin uses custom proto options from `open.alis.services/protobuf`:

### File-Level Options

```protobuf
import "alis/open/options/v1/options.proto";

// Enable schema generation for all messages in this file
option (alis.open.options.v1.file).json_schema.generate = true;
```

Extracted by: `getFileJsonSchemaOptions(file *protogen.File)`

### Message-Level Options

```protobuf
message User {
  option (alis.open.options.v1.message).json_schema.generate = true;
  // ...
}
```

Extracted by: `getMessageJsonSchemaOptions(message *protogen.Message)`

### Field-Level Options

```protobuf
message User {
  string email = 1 [(alis.open.options.v1.field).json_schema = {
    format: "email"
    title: "Email Address"
    description: "User's primary email"
    min_length: 5
    max_length: 255
  }];
}
```

Extracted by: `getFieldJsonSchemaOptions(field *protogen.Field)`

Available field options:
- `ignore` - Exclude from schema
- `title`, `description` - Metadata
- `format`, `pattern` - String validation
- `minimum`, `maximum`, `exclusive_minimum`, `exclusive_maximum` - Numeric bounds
- `min_length`, `max_length` - String length
- `min_items`, `max_items`, `unique_items` - Array constraints
- `min_properties`, `max_properties` - Object constraints
- `content_encoding`, `content_media_type` - Binary data hints

---

## Testing Patterns

### Test Suite Hierarchy

```
PluginTestSuite (base)
    │
    ├── PluginGeneratorTestSuite (plugin_test.go)
    │   └── Tests for Generator and Generate function
    │
    ├── FunctionsTestSuite (functions_test.go)
    │   └── Unit tests for helper functions
    │
    └── IntegrationTestSuite (integration_test.go)
        └── End-to-end tests with protoc
```

### Base Test Suite (`PluginTestSuite`)

Located in `plugin/suite_test.go`. Provides:

- **Setup**: Finds workspace root, regenerates descriptor sets from proto files
- **Fixtures**: `fds` (FileDescriptorSet), `plugin` (protogen.Plugin), `file` (target file)
- **Helpers**: `FindMessage()`, `FindField()`, `RunGenerate()`, `GetGeneratedContent()`

```go
// Example test using the suite
func (s *FunctionsTestSuite) TestSomething() {
    msg := s.FindMessage("User")
    field := s.FindField(msg, "email")
    // ... test logic
}
```

### Golden File Testing

Integration tests compare generated output against golden files:

```go
func (s *IntegrationTestSuite) TestGoldenFile() {
    contents := s.RunGenerate()
    for name, content := range contents {
        goldenPath := filepath.Join(goldenDir(), baseName+".golden")
        assertGoldenFile(s.T(), content, goldenPath, *updateGolden)
    }
}
```

Update golden files with:
```shell
go test ./plugin/... -update
```

### Test Utilities (`testutil_test.go`)

Common helpers:
- `loadDescriptorSet()` - Load FileDescriptorSet from .pb file
- `createTestPlugin()` - Create protogen.Plugin for testing
- `generateDescriptorSet()` - Run protoc to generate descriptor set
- `assertGoldenFile()` - Compare against golden file (with timestamp normalization)
- `mustFindFile()`, `mustFindMessage()`, `mustFindField()` - Find proto elements

### Running Tests

```shell
# Run all tests
go test ./...

# Run with verbose output
go test -v ./...

# Run specific suite
go test -v ./plugin/... -run TestFunctionsSuite

# Update golden files
go test ./plugin/... -update

# Skip long-running tests
go test -short ./...
```

---

## Development Commands

### Building

```shell
# Build for current platform
go build ./...

# Build the plugin binary
go build -o protoc-gen-go-jsonschema ./cmd/protoc-gen-go-jsonschema

# Build for all platforms (releases)
./build.sh v1.0.0
```

### Testing

```shell
# Run all tests
go test ./...

# Run with race detector
go test -race ./...

# Update golden files
go test ./plugin/... -update

# Run specific test
go test -v ./plugin/... -run TestEscapeGoString
```

### Using the Plugin Locally

```shell
# Build and install to GOPATH/bin
go install ./cmd/protoc-gen-go-jsonschema

# Or run protoc with explicit plugin path
protoc --plugin=protoc-gen-go-jsonschema=./protoc-gen-go-jsonschema \
       --go-jsonschema_out=. \
       your.proto
```

---

## Code Patterns and Conventions

### Generated Code Pattern

Each message generates two functions:

```go
// Public entry point - returns complete schema with bundled definitions
func (x *MessageName) JsonSchema() *jsonschema.Schema {
    defs := make(map[string]*jsonschema.Schema)
    _ = MessageName_JsonSchema_WithDefs(defs)
    root := defs["package.MessageName"]
    root.Definitions = defs
    return root
}

// Internal helper - populates shared definitions map, returns $ref
func MessageName_JsonSchema_WithDefs(defs map[string]*jsonschema.Schema) *jsonschema.Schema {
    // Early return if already defined (prevents infinite recursion)
    if _, ok := defs["package.MessageName"]; ok {
        return &jsonschema.Schema{Ref: "#/$defs/package.MessageName"}
    }
    
    schema := &jsonschema.Schema{
        Type: "object",
        Properties: make(map[string]*jsonschema.Schema),
        // ...
    }
    
    // Register BEFORE processing fields (handles self-references)
    defs["package.MessageName"] = schema
    
    // Generate field schemas...
    
    return &jsonschema.Schema{Ref: "#/$defs/package.MessageName"}
}
```

### Adding New Field Type Support

1. Update `getKindTypeName()` if it's a new proto kind
2. Add handling in appropriate config builder (`getScalarSchemaConfig`, `getArraySchemaConfig`, or `getMapSchemaConfig`)
3. If it's a WKT, add case to `getMessageSchemaConfig()`
4. Add tests in `functions_test.go`

### Adding New Option Support

1. Check option proto definition in `open.alis.services/protobuf`
2. Add handling in `emitSchemaField()` (for field options)
3. Add tests verifying the option is applied

---

## Common Issues and Solutions

### Circular References

The plugin handles circular message references through:
1. Registering schema in `defs` BEFORE processing fields
2. Checking `visited` map to avoid re-processing
3. Returning `$ref` pointers instead of inline definitions

### 64-bit Integer Precision

JavaScript cannot safely represent integers beyond 2^53-1. The plugin:
- Maps int64/uint64 to `"string"` type
- Adds pattern `"^-?[0-9]+$"` for validation
- This matches proto3 JSON encoding behavior

### WKT vs User Messages

Well-Known Types are handled inline (no `$ref`), while user-defined messages use `$ref` to `$defs`. Check `getMessageSchemaConfig()` for the distinction.

---

## File Locations Quick Reference

| What | Where |
|------|-------|
| Plugin entry point | `cmd/protoc-gen-go-jsonschema/main.go` |
| Generation logic | `plugin/functions.go` |
| Type constants | `plugin/functions.go` (top of file) |
| WKT handling | `plugin/functions.go` → `getMessageSchemaConfig()` |
| Options extraction | `plugin/functions.go` → `getField/Message/FileJsonSchemaOptions()` |
| Test fixtures | `testdata/protos/users/v1/user.proto` |
| Golden files | `testdata/golden/*.golden` |
| Base test suite | `plugin/suite_test.go` |
