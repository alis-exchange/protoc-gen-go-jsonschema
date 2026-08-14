// Package plugin provides the core functionality for the protoc-gen-go-jsonschema plugin.
//
// This package converts Protocol Buffer message definitions into Go code that generates
// JSON Schema (Draft 2020-12) representations at runtime. The generated code creates
// schema functions for each targeted message that can be used for validation, documentation,
// or API specification generation.
//
// # Architecture
//
// The plugin separates deciding what a schema is from printing Go source:
//
//  1. Message Collection (collect.go): Scans proto files to identify messages that
//     should generate schemas based on file-level and message-level options, forcing
//     dependencies and nested messages so $refs always resolve.
//  2. Schema Model (model.go): buildMessageSchema turns one message plus its options
//     into a complete, symbolic schema model — every keyword, required-field, oneof
//     shape, and naming decision is made here. A messageIdentity value answers every
//     naming question (defs key, function name, method vs standalone, oneof strategy)
//     exactly once.
//  3. Printer (printer.go): Walks the model and emits the generated Go source. It
//     knows Go syntax, not schema rules.
//
// # Generated Code Structure
//
// For each message, two functions are generated:
//   - JsonSchema() - Public method that returns a complete schema with definitions
//     (or standalone function for Google types: google_protobuf_Timestamp_JsonSchema())
//   - <MessageName>_JsonSchema_WithDefs() - Internal function for recursive schema building
//
// # Type Mapping
//
// Protocol Buffer types are mapped to JSON Schema types following the proto3 JSON mapping:
//   - Scalar types (int32, string, bool, etc.) → Corresponding JSON Schema types
//   - 64-bit integers → integer type
//   - bytes → string with base64 contentEncoding
//   - Enums → integer type with enum constraint (numeric values for encoding/json compatibility)
//   - Messages → object type with properties, or $ref for cross-references
//   - Repeated fields → array type
//   - Map fields → object type with additionalProperties
//   - Oneofs (user messages) → nested PascalCase wrapper properties matching encoding/json
//     (Google types keep flat oneof properties with proto JSON semantics)
//
// # Google Types
//
// All Google types (google.protobuf.*, google.type.*, google.api.*, google.iam.*, etc.)
// are handled like normal messages, generating standalone functions (not methods) since
// they're imported types. Google type schemas are generated in the file where they're referenced.
//
// # Options
//
// The plugin supports custom options at file, message, and field levels to control
// schema generation, add validation constraints, and customize metadata.
package plugin
