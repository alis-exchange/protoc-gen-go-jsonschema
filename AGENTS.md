# AI Agent Guide for protoc-gen-go-jsonschema

This document provides comprehensive information for AI agents and LLMs working with this codebase.

## ⚠️ IMPORTANT: Documentation Maintenance

**LLMs and AI agents MUST update this document when making significant changes to the plugin.**

Significant changes include:

- New features or capabilities
- Changes to message generation logic
- New options or option behaviors
- Bug fixes that change behavior
- New test patterns or testing approaches
- Changes to the code generation output format

When updating this document:

1. Update the relevant sections with new information
2. Add new sections if needed
3. Update the "File Locations Quick Reference" table
4. Update the "Common Issues and Solutions" section if applicable
5. Keep examples and code snippets current

**This document is the single source of truth for understanding how the plugin works.**

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
// Generated code example (ref-as-root pattern)
func (x *User) JsonSchema() *jsonschema.Schema {
    defs := make(map[string]*jsonschema.Schema)
    _ = User_JsonSchema_WithDefs(defs)
    root := &jsonschema.Schema{Ref: "#/$defs/package.User"}
    root.Defs = defs
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
│   ├── testutils.go             # TestingHelper (build-tagged plugintest)
├── plugin_test/
│   ├── suite.go                 # PluginTestSuite, IntegrationTestSuite base
│   ├── testutil.go              # assertGoldenFile, loadDescriptorSet, etc.
│   ├── integration_test.go      # End-to-end integration tests
│   ├── plugin_test.go           # Generator and plugin tests
│   └── functions_test.go        # Unit tests for helper functions
├── testdata/
│   ├── protos/                  # Sample proto files for testing
│   │   ├── users/v1/user.proto
│   │   └── force_test/v1/force_test.proto  # Test proto for force logic
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
- `getMessages()` - Public wrapper that calls `getMessagesWithForce()` with `force=false`
- `getMessagesWithForce()` - Internal implementation with force logic for dependencies and nested messages
- `escapeGoString()` - Escapes strings for Go source code
- `getTitleAndDescription()` - Extracts metadata from proto comments

#### `MessageSchemaGenerator` (plugin/functions.go)

Stateful per-message schema builder:

- `gr` - Reference to parent Generator
- `gen` - Output file writer (`*protogen.GeneratedFile`)
- `visited` - Map tracking processed messages (prevents infinite recursion)
- `filePrefix` - Proto file name prefix for unique Google type function names

Key methods:

- `generateMessageJSONSchema()` - Generates complete schema for a message
- `generateFieldJSONSchema()` - Routes to appropriate config builder
- `emitSchemaField()` - Generates Go code for a field's schema
- `getArraySchemaConfig()` - Creates config for repeated fields
- `getMapSchemaConfig()` - Creates config for map fields
- `getScalarSchemaConfig()` - Creates config for scalar/message fields
- `getMessageSchemaConfig()` - Handles Google types and message references
- `getKindTypeName()` - Maps proto kinds to JSON Schema types

#### `schemaFieldConfig` (plugin/functions.go)

Intermediate representation for field schemas:

```go
type schemaFieldConfig struct {
    fieldName            string  // Proto field name (snake_case)
    title                string  // Schema title
    description          string  // Schema description
    typeName             string  // JSON Schema type ("string", "object", etc.)
    format               string  // Format annotation ("date-time", "email", etc.)
    pattern              string  // Regex pattern
    propertyNamesPattern string  // Pattern for map keys
    enumValues           []int32  // Allowed enum values (numeric for encoding/json compatibility)
    isBytes              bool    // Requires base64 contentEncoding
    messageRef           string  // Reference function call for messages
    nested               *schemaFieldConfig // For array items / map values
}
```

---

## Type Mapping Reference

### Field Names

Generated schemas use **proto field names** (snake_case) instead of JSON names (camelCase). This is because agents and MCP tools typically use `json.Marshal` instead of `protojson.Marshal`.

```protobuf
// Proto field definition
message User {
  string first_name = 1;  // Proto field name: first_name
  string last_name = 2;   // Proto field name: last_name
}
```

```go
// Generated schema uses snake_case
schema.Properties["first_name"] = &jsonschema.Schema{Type: "string"}
schema.Properties["last_name"] = &jsonschema.Schema{Type: "string"}
```

The `getFieldName()` helper function returns the proto field name directly via `field.Desc.Name()`.

### Scalar Types

| Proto Type                    | JSON Schema Type | Additional Constraints            |
| ----------------------------- | ---------------- | --------------------------------- |
| `string`                      | `"string"`       | —                                 |
| `bool`                        | `"boolean"`      | —                                 |
| `int32`, `sint32`, `sfixed32` | `"integer"`      | —                                 |
| `uint32`, `fixed32`           | `"integer"`      | —                                 |
| `int64`, `sint64`, `sfixed64` | `"integer"`      | —                                 |
| `uint64`, `fixed64`           | `"integer"`      | —                                 |
| `float`                       | `"number"`       | —                                 |
| `double`                      | `"number"`       | —                                 |
| `bytes`                       | `"string"`       | `contentEncoding: "base64"`       |
| `enum`                        | `"integer"`      | `enum: [0, 1, 2, ...]` (numeric values for encoding/json) |

**Note**: 64-bit integers are mapped to `"integer"` type for simplicity. While JavaScript has precision limitations for large integers (beyond 2^53-1), most use cases don't require values that large, and using `"integer"` provides better schema validation.

### Complex Types

| Proto Type   | JSON Schema Type | Structure                                          |
| ------------ | ---------------- | -------------------------------------------------- |
| `message`    | `"object"`       | Properties for each field, or `$ref`               |
| `repeated T` | `"array"`        | `items` contains element schema                    |
| `map<K, V>`  | `"object"`       | `additionalProperties` contains value schema       |
| `oneof`      | —                | `oneOf` constraint with `required` for each option |

### Required Fields

A field is added to the JSON Schema `required` array only if **all** of the following are true:

- Not in a `oneof` group
- Not marked with the `optional` keyword
- Not a `repeated` field (array)
- Not a `map` field

This means repeated fields and map fields are always optional in the generated schema, which aligns with how these types work in practice (an empty array `[]` or empty object `{}` is valid).

### Map Key Handling

Map keys are always strings in JSON. Non-string proto keys use `propertyNames` validation:

| Proto Key Type         | propertyNames Pattern |
| ---------------------- | --------------------- |
| `string`               | (none)                |
| `int32`, `int64`, etc. | `"^-?[0-9]+$"`        |
| `bool`                 | `"^(true\|false)$"`   |

---

## Google Types

All Google types (any message in a `google.*` package) are treated like normal messages and generate schemas with `$ref` definitions. Since Google types are imported types (we can't add methods to them), the plugin generates **standalone functions** instead of methods.

This includes:

- Well-known types: `google.protobuf.*` (Timestamp, Duration, Any, Struct, etc.)
- Common types: `google.type.*` (Date, LatLng, Money, etc.)
- API types: `google.api.*` (HttpBody, ResourceDescriptor, etc.)
- IAM types: `google.iam.*` (ServiceAccountKey, Policy, etc.)
- Any other `google.*` packages

### Google Type Function Naming

Google type functions include a **file prefix** to ensure uniqueness when multiple proto files in the same package import the same types:

```go
// From user.proto
func user_google_protobuf_Timestamp_JsonSchema() *jsonschema.Schema { ... }
func user_google_protobuf_Timestamp_JsonSchema_WithDefs(defs map[string]*jsonschema.Schema) *jsonschema.Schema { ... }

// From admin.proto (same package)
func admin_google_protobuf_Timestamp_JsonSchema() *jsonschema.Schema { ... }
func admin_google_protobuf_Timestamp_JsonSchema_WithDefs(defs map[string]*jsonschema.Schema) *jsonschema.Schema { ... }

// IAM types also work the same way
func common_google_iam_admin_v1_ServiceAccountKey_JsonSchema() *jsonschema.Schema { ... }
```

The prefix is derived from the proto file name (e.g., `users/v1/admin.proto` → `admin`).

### Google Type Helper Functions

Located in `plugin/functions.go`:

- `isGoogleType(msg)` - Checks if a message is from a Google package (`google.*`)
- `googleTypeFunctionName(msg, filePrefix)` - Generates the function name with file prefix
- `fileNamePrefix(file)` - Extracts prefix from proto file path

### MessageSchemaGenerator.filePrefix

The `MessageSchemaGenerator` struct includes a `filePrefix` field that is set during file generation and used for Google type function naming.

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

**Important: Force Logic for Dependencies and Nested Messages**

When a message has `generate = true`, its **field dependencies** and **nested messages** are **forced to generate** even if they have `generate = false`. This ensures that `$ref` pointers in the generated schema can always be resolved.

```protobuf
message Parent {
  option (alis.open.options.v1.message).json_schema.generate = true;

  Dependency dep = 1;  // Field dependency - will generate even if generate=false

  message Nested {
    option (alis.open.options.v1.message).json_schema.generate = false;  // Explicit false
    string value = 1;
  }

  Nested nested = 2;  // Nested message - will generate even if generate=false
}

message Dependency {
  option (alis.open.options.v1.message).json_schema.generate = false;  // Explicit false
  string data = 1;
}
```

In the above example, both `Dependency` and `Parent.Nested` will generate schemas because `Parent` has `generate = true`. This prevents broken `$ref` pointers like `"#/$defs/package.Parent.Nested"` that point to non-existent definitions.

The force logic is implemented in `getMessagesWithForce()`:

- Field dependencies: Called with `force=true` (line 260)
- Nested messages: Called with `force=true` (line 280)
- When `force=true` and a message has `generate=false`, the `false` is ignored and `defaultGenerate` (which is `true` when forcing) is used instead

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

Located in `plugin_test/suite.go`. Provides:

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
// Public entry point - returns ref-as-root schema with bundled definitions
func (x *MessageName) JsonSchema() *jsonschema.Schema {
    defs := make(map[string]*jsonschema.Schema)
    _ = MessageName_JsonSchema_WithDefs(defs)
    root := &jsonschema.Schema{Ref: "#/$defs/package.MessageName"}
    root.Defs = defs
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
3. Add tests in `functions_test.go`

Note: Google types are now handled like normal messages (no special cases needed).

### Adding New Option Support

1. Check option proto definition in `open.alis.services/protobuf`
2. Add handling in `emitSchemaField()` (for field options)
3. Add tests verifying the option is applied

---

## Common Issues and Solutions

### Circular References and Recursive Types

The plugin handles circular/recursive message references through:

1. Registering schema in `defs` BEFORE processing fields
2. Checking `visited` map to avoid re-processing
3. Returning `$ref` pointers instead of inline definitions
4. **Ref-as-root pattern**: The `JsonSchema()` method returns a `$ref` wrapper (`root := &jsonschema.Schema{Ref: "#/$defs/..."}`) with `root.Defs = defs`. The actual schema stays in `defs`, so `root != defs[key]` (no pointer cycle). This both prevents stack overflow on JSON marshaling and enables recursive types like `AddressDetails` whose properties reference `#/$defs/AddressDetails` (the def must exist for refs to resolve).

### Nested Messages with generate=false

**Problem**: Nested messages with `generate = false` might not generate, causing broken `$ref` pointers.

**Solution**: The force logic ensures that when a parent message has `generate = true`, all nested messages are forced to generate regardless of their own `generate` option. This is implemented in `getMessagesWithForce()` with `force=true` for nested message processing.

**Example**:

```protobuf
message Parent {
  option (alis.open.options.v1.message).json_schema.generate = true;

  message Nested {
    option (alis.open.options.v1.message).json_schema.generate = false;  // Still generates!
    string value = 1;
  }
}
```

### Field Dependencies with generate=false

**Problem**: Message-type fields referencing messages with `generate = false` might cause broken `$ref` pointers.

**Solution**: Field dependencies are forced to generate when the parent generates. This is implemented in `getMessagesWithForce()` with `force=true` for field dependency processing.

### 64-bit Integer Handling

64-bit integers (`int64`, `uint64`, `sint64`, `sfixed64`, `fixed64`) are mapped to JSON Schema `"integer"` type. While JavaScript has precision limitations for very large integers (beyond 2^53-1), the `"integer"` type provides better schema validation and works correctly for most use cases.

### Google Type Handling

All Google types (`google.*`) are treated like normal messages and generate schemas with `$ref` definitions. Key differences from user messages:

1. **Standalone functions**: Google types generate standalone functions (not methods) since we can't add methods to imported types
2. **File prefix**: Google type function names include a file prefix (e.g., `user_google_protobuf_Timestamp_JsonSchema`) to avoid duplicate function names when multiple files in the same package import the same types
3. **Recursive dependencies**: Google type dependencies (including map value types) are properly collected via `getMessagesWithForce()`

### Map Value Dependencies

**Problem**: Map fields with message value types (e.g., `map<string, Value>`) might not collect the value message as a dependency.

**Solution**: The `getMessagesWithForce()` function now handles map fields specially:

- For map fields, it extracts the value message from the synthetic map entry (field number 2)
- This ensures Google type dependencies like `google.protobuf.Value` (used by `Struct.fields`) are properly collected

### Multi-File Packages

**Problem**: When multiple proto files share the same Go package (e.g., `common.proto`, `admin.proto`, `user.proto` all using `go_package = "github.com/example/users/v1;usersv1"`), you must compile all proto files together.

**Solution**: The plugin filters messages by their source proto file using `msg.Desc.ParentFile().Path()`. Each proto file generates schemas only for messages **defined in that file**. Messages from other files in the same package are referenced by their `_WithDefs` function name.

**Example**:

- `common.proto` generates `Common_JsonSchema_WithDefs` in `common_jsonschema.pb.go`
- `admin.proto` generates `Admin_JsonSchema_WithDefs` in `admin_jsonschema.pb.go`
- `admin_jsonschema.pb.go` calls `Common_JsonSchema_WithDefs(defs)` to reference Common

**Important**: All proto files in a shared Go package must be compiled together so that cross-file references can be resolved at compile time.

---

## File Locations Quick Reference

| What                          | Where                                                                                    |
| ----------------------------- | ---------------------------------------------------------------------------------------- |
| Plugin entry point            | `cmd/protoc-gen-go-jsonschema/main.go`                                                   |
| Generation logic              | `plugin/functions.go`                                                                    |
| Ref-as-root generation        | `plugin/functions.go` → `generateMessageJSONSchema()` (root := &jsonschema.Schema{Ref: ...}) |
| Type constants                | `plugin/functions.go` (top of file)                                                      |
| Message collection            | `plugin/functions.go` → `getMessages()` / `getMessagesWithForce()`                       |
| Force logic                   | `plugin/functions.go` → `getMessagesWithForce()`                                         |
| Field name helper             | `plugin/functions.go` → `getFieldName()`                                                 |
| Google type helpers           | `plugin/functions.go` → `isGoogleType()`, `googleTypeFunctionName()`, `fileNamePrefix()` |
| Google type schema generation | `plugin/functions.go` → `generateMessageJSONSchema()` (check `isGoogleType()`)           |
| Options extraction            | `plugin/functions.go` → `getField/Message/FileJsonSchemaOptions()`                       |
| Test fixtures                 | `testdata/protos/users/v1/user.proto`                                                    |
| Force logic tests             | `testdata/protos/force_test/v1/force_test.proto`                                         |
| Golden files                  | `testdata/golden/*.golden`                                                               |
| Base test suite               | `plugin_test/suite.go`                                                                   |
| Force logic unit tests        | `plugin_test/plugin_test.go` → `TestGetMessagesWithForce()`                             |
| Force logic integration tests | `plugin_test/integration_test.go` → `TestForceLogic*()`                                  |
| Debug tests (multi-file)      | `debug/debug_test.go`                                                                    |
