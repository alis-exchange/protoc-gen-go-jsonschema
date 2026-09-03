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
//     dependencies and nested messages so every generated reference resolves.
//  2. Cycle Analysis (analyze.go): Tarjan SCC over the message-reference graph
//     decides each message's mode — inline (acyclic, the common case) or $defs
//     (on a reference cycle, the only case inlining cannot express).
//  3. Schema Model (model.go): buildMessageSchema turns one message plus its options
//     into a complete, symbolic schema model — every keyword, required-field, oneof
//     shape, and naming decision is made here. A messageIdentity value answers every
//     naming and strategy question (defs key, function name, method vs standalone,
//     oneof strategy, inline vs $defs) exactly once.
//  4. Printer (printer.go): Walks the model and emits the generated Go source. It
//     knows Go syntax, not schema rules.
//
// # Generated Code Structure
//
// For each message, two functions are generated (three for messages on a cycle):
//   - JsonSchema() - Public method that returns a complete schema whose root is
//     always a literal object (or standalone function for Google types:
//     google_protobuf_Timestamp_JsonSchema())
//   - <MessageName>_JsonSchema_WithDefs(defs) - Composition helper: returns a fresh
//     inline object schema, or — for messages on a cycle — registers the definition
//     under defs and returns a $ref to it
//   - <MessageName>_JsonSchema_build(defs, register) - Cyclic messages only: the
//     schema body, run once to register the definition and once to build an
//     independent root (jsonschema-go resolution requires a tree)
//
// # Type Mapping
//
// Protocol Buffer types are mapped to JSON Schema types following the proto3 JSON mapping:
//   - Scalar types (int32, string, bool, etc.) → Corresponding JSON Schema types
//   - 64-bit integers → integer type
//   - bytes → string with base64 contentEncoding
//   - Enums → integer type with enum constraint (numeric values for encoding/json compatibility)
//   - Messages → object type with properties, inlined; $ref into $defs only for
//     messages on a reference cycle
//   - google.protobuf.Struct/Value/ListValue → free-form JSON (their Go types marshal
//     as plain JSON); Value lists every JSON type rather than emitting an empty schema
//   - Repeated fields → array type
//   - Map fields → object type with additionalProperties
//   - Oneofs → nested PascalCase wrapper properties matching encoding/json, for Google
//     types too (none of them has a custom json.Marshaler once the free-form three are
//     excluded)
//
// # Google Types
//
// All Google types (google.protobuf.*, google.type.*, google.api.*, google.iam.*, etc.)
// are handled like normal messages, generating standalone functions (not methods) since
// they're imported types. Google type schemas are generated in the file where they're
// referenced. Struct, Value and ListValue are the exception: they generate nothing and
// inline as free-form nodes.
//
// # Options
//
// The plugin supports custom options at file, message, and field levels to control
// schema generation, add validation constraints, and customize metadata.
package plugin
