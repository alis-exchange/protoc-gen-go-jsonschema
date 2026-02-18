// Package plugin provides the core functionality for the protoc-gen-go-jsonschema plugin.
//
// This package converts Protocol Buffer message definitions into Go code that generates
// JSON Schema (Draft 2020-12) representations at runtime. The generated code creates
// schema functions for each targeted message that can be used for validation, documentation,
// or API specification generation.
//
// # Architecture
//
// The plugin follows a two-phase approach:
//  1. Message Collection: Scans proto files to identify messages that should generate schemas
//     based on file-level and message-level options.
//  2. Code Generation: For each target message, generates Go functions that construct
//     JSON Schema objects using the github.com/google/jsonschema-go/jsonschema package.
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

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	optionsPb "open.alis.services/protobuf/alis/open/options/v1"
)

// -----------------------------------------------------------------------------
// JSON Schema Type Constants
// -----------------------------------------------------------------------------
//
// These constants represent the primitive type names defined in JSON Schema Draft 2020-12.
// See: https://json-schema.org/draft/2020-12/json-schema-validation.html#section-6.1.1
//
// The "type" keyword in JSON Schema restricts the instance to one of these seven primitive types.
// When mapping Protocol Buffer types, we use these constants to ensure consistency and
// avoid typos in the generated schema code.
const (
	jsArray   = "array"   // JSON array type - used for repeated fields
	jsBoolean = "boolean" // JSON boolean type - used for bool fields
	jsInteger = "integer" // JSON integer type - used for 32-bit integer fields
	jsNull    = "null"    // JSON null type - used in nullable type unions
	jsNumber  = "number"  // JSON number type - used for float/double fields
	jsObject  = "object"  // JSON object type - used for messages and maps
	jsString  = "string"  // JSON string type - used for strings and bytes
)

// isGoogleType checks if a message is from a Google package (google.*).
// This includes well-known types (google.protobuf.*), common types (google.type.*),
// API types (google.api.*), IAM types (google.iam.*), and any other google.* packages.
// These are treated specially because we cannot add methods to imported types,
// so we generate standalone functions with file prefixes instead.
func isGoogleType(msg *protogen.Message) bool {
	return strings.HasPrefix(string(msg.Desc.FullName()), "google.")
}

// googleTypeFunctionName converts a Google type's full name to a valid Go function name with a file prefix.
// The prefix ensures uniqueness when multiple files in the same package import the same Google types.
// Example: "google.protobuf.Timestamp" with prefix "admin" -> "admin_google_protobuf_Timestamp"
func googleTypeFunctionName(msg *protogen.Message, filePrefix string) string {
	fullName := string(msg.Desc.FullName())
	baseName := strings.ReplaceAll(fullName, ".", "_")
	if filePrefix != "" {
		return filePrefix + "_" + baseName
	}
	return baseName
}

// fileNamePrefix extracts a prefix from the proto file path for use in Google type function names.
// Example: "users/v1/admin.proto" -> "admin"
func fileNamePrefix(file *protogen.File) string {
	// Get the base name without extension
	path := file.Desc.Path()
	base := strings.TrimSuffix(path, ".proto")
	// Get just the file name part (after the last /)
	if idx := strings.LastIndex(base, "/"); idx >= 0 {
		base = base[idx+1:]
	}
	return base
}

// -----------------------------------------------------------------------------
// Core Types
// -----------------------------------------------------------------------------

// Generator is the main entry point for the protoc-gen-go-jsonschema plugin.
// It coordinates the overall code generation process by:
//   - Processing plugin options to determine which messages to generate
//   - Creating output files with proper headers and imports
//   - Delegating message-level generation to MessageSchemaGenerator
//
// Generator is stateless; all state is passed through method parameters or
// held in MessageSchemaGenerator for per-message generation.
type Generator struct {
	// Version is the plugin version used to generate this file
	Version string
}

// -----------------------------------------------------------------------------
// Generator Methods
// -----------------------------------------------------------------------------

// generateFile creates a Go source file containing JSON Schema generation code
// for all targeted messages in the given proto file.
//
// The generation process:
//  1. Checks file-level options to determine the default generation behavior
//  2. Collects all messages that should generate schemas (respecting options)
//  3. Creates the output file with standard headers and imports
//  4. Generates schema code for each target message
//
// Returns nil if no messages in the file require schema generation.
func (gr *Generator) generateFile(gen *protogen.Plugin, file *protogen.File) (*protogen.GeneratedFile, error) {
	// --- Determine Generation Scope ---
	// Check file-level options to see if all messages should generate schemas by default.
	// Individual messages can override this with their own options.
	generateAll := false
	if opts := getFileJsonSchemaOptions(file); opts != nil {
		generateAll = opts.GetGenerate()
	}

	// Collect messages that should generate schemas, including their dependencies.
	// The visited map prevents processing the same message twice.
	// This includes cross-package messages to ensure the defs map is complete.
	targetMessages := gr.getMessages(file.Messages, generateAll, make(map[string]bool))

	// --- CRITICAL: Filter to only messages DEFINED in THIS proto file ---
	//
	// Why: When multiple proto files share the same Go package and import each other
	// (or import the same shared protos), we would generate duplicate function
	// definitions if we filter by Go package instead of proto file.
	//
	// Solution: Only generate schema functions for messages defined in THIS proto file.
	// Messages from other proto files (even in the same Go package) are just referenced
	// by their _WithDefs function name - they will be generated in their own file.
	// Cross-package messages are automatically referenced via QualifiedGoIdent.
	// Google types are generated in the file where they're referenced (with file prefix).
	var localMessages []*protogen.Message
	var googleTypeMessages []*protogen.Message
	for _, msg := range targetMessages {
		// Include only messages DEFINED in this proto file (not just same Go package)
		// Note: Use Path() not FullName() - FullName() returns the package name for files
		if msg.Desc.ParentFile().Path() == file.Desc.Path() {
			localMessages = append(localMessages, msg)
		} else if isGoogleType(msg) {
			// Include Google types that are referenced (they'll be generated as standalone functions)
			googleTypeMessages = append(googleTypeMessages, msg)
		}
	}

	// Skip file generation entirely if no local messages or Google types need schemas.
	// This avoids creating empty or import-only files.
	if len(localMessages) == 0 && len(googleTypeMessages) == 0 {
		return nil, nil
	}

	// --- Create Output File ---
	// Generate filename following the pattern: <original>_jsonschema.pb.go
	filename := file.GeneratedFilenamePrefix + "_jsonschema.pb.go"
	g := gen.NewGeneratedFile(filename, file.GoImportPath)

	// Write file header with generation metadata.
	// This helps identify generated files and track their source.
	{
		g.P("// Code generated by https://github.com/alis-exchange/protoc-gen-go-jsonschema. DO NOT EDIT.")
		g.P("// ")
		g.P(fmt.Sprintf("// Source: %s", file.Desc.Path()))
		g.P(fmt.Sprintf("// Plugin version: %s", gr.Version))
		g.P("// ")
		g.P(fmt.Sprintf("// Generated on: %s UTC", time.Now().UTC().Format("2006-01-02 15:04:05")))
	}

	// Write package declaration matching the proto's go_package option.
	g.P()
	g.P(fmt.Sprintf("package %s", file.GoPackageName))

	// Write imports. Additional imports may be added automatically by protogen
	// when QualifiedGoIdent is used during code generation.
	{
		g.P("import (")
		g.P("\"github.com/google/jsonschema-go/jsonschema\"")
		g.P(")")
		g.P()
	}

	// --- Generate Message Schemas (LOCAL MESSAGES AND REFERENCED GOOGLE TYPES) ---
	// Process each local message, creating a fresh MessageSchemaGenerator
	// for each to ensure clean visited state tracking.
	// Cross-package messages are referenced (not generated) via QualifiedGoIdent.
	// Google types are generated as standalone functions in the file where they're referenced.
	prefix := fileNamePrefix(file)
	for _, msg := range localMessages {
		sg := &MessageSchemaGenerator{
			gr:         gr,
			gen:        g,
			visited:    make(map[string]bool),
			filePrefix: prefix,
		}
		if err := sg.generateMessageJSONSchema(msg); err != nil {
			return nil, err
		}
		g.P()
	}

	// Generate Google type schemas as standalone functions
	for _, msg := range googleTypeMessages {
		sg := &MessageSchemaGenerator{
			gr:         gr,
			gen:        g,
			visited:    make(map[string]bool),
			filePrefix: prefix,
		}
		if err := sg.generateMessageJSONSchema(msg); err != nil {
			return nil, err
		}
		g.P()
	}

	return g, nil
}

// getMessages recursively collects all messages that should generate JSON Schema code.
//
// This method implements the message filtering and dependency resolution logic:
//   - Skips internal proto types (map entries)
//   - Respects message-level options that can override the default generation flag
//   - Automatically includes message dependencies (fields that reference other messages)
//   - Recursively processes nested message definitions
//   - Includes Google types (google.*) when referenced
//
// Parameters:
//   - messages: The list of messages to process (top-level or nested)
//   - defaultGenerate: Whether to generate by default (from file or parent options)
//   - visited: Tracks already-processed messages to prevent duplicates and infinite loops
//
// Returns a flat list of all messages that should generate schemas, in dependency order.
func (gr *Generator) getMessages(messages []*protogen.Message, defaultGenerate bool, visited map[string]bool) []*protogen.Message {
	return gr.getMessagesWithForce(messages, defaultGenerate, false, visited)
}

// getMessagesWithForce is the internal implementation that supports forcing generation.
// When force=true, explicit generate=false options are ignored to prevent broken $refs.
func (gr *Generator) getMessagesWithForce(messages []*protogen.Message, defaultGenerate bool, force bool, visited map[string]bool) []*protogen.Message {
	var results []*protogen.Message

	for _, message := range messages {
		// --- Skip Non-User Messages ---
		{
			// Map entries are synthetic messages created by protoc for map fields.
			// We handle maps directly in the field processing, not as separate messages.
			if message.Desc.IsMapEntry() {
				continue
			}
		}

		// --- Determine Generation Flag ---
		// Start with the inherited default, then check for message-specific override.
		// If force=true, ignore explicit generate=false to prevent broken $refs.
		shouldGen := defaultGenerate
		if opts := getMessageJsonSchemaOptions(message); opts != nil {
			optValue := opts.GetGenerate()
			// When forced (e.g., by parent generating), ignore explicit false
			if force && !optValue {
				// Keep defaultGenerate (forced true), don't override to false
				shouldGen = defaultGenerate
			} else {
				shouldGen = optValue
			}
		}

		// --- Process Message if Enabled ---
		if shouldGen {
			messageName := string(message.Desc.FullName())

			// Only process each message once to avoid duplicates and infinite recursion.
			if !visited[messageName] {
				visited[messageName] = true
				results = append(results, message)

				// Recursively collect dependencies: any message-type field must also
				// generate a schema, otherwise the $ref in the parent would be broken.
				// We force 'true' here because dependencies are required regardless
				// of their own options.
				for _, field := range message.Fields {
					if field.Desc.Kind() == protoreflect.MessageKind {
						// For map fields, we need to collect the value type, not the synthetic map entry
						if field.Desc.IsMap() {
							mapValue := field.Desc.MapValue()
							if mapValue.Kind() == protoreflect.MessageKind {
								// Find the value message from the synthetic map entry
								for _, f := range field.Message.Fields {
									if f.Desc.Number() == 2 && f.Message != nil { // Field 2 is the value
										depMessages := gr.getMessagesWithForce([]*protogen.Message{f.Message}, true, true, visited)
										results = append(results, depMessages...)
										break
									}
								}
							}
						} else {
							depMessages := gr.getMessagesWithForce([]*protogen.Message{field.Message}, true, true, visited)
							results = append(results, depMessages...)
						}
					}
				}
			}

			// --- Process Nested Messages (ONLY when parent generates) ---
			// CRITICAL: Force generation for nested messages when parent generates.
			// This ensures $ref pointers like "#/$defs/Parent.Child" can be resolved.
			//
			// Rationale:
			// - Nested messages are part of the parent's type system and cannot exist independently
			// - If parent generates, all referenced nested types MUST generate
			// - Parent fields will create $ref pointers to nested messages in $defs
			// - Without forcing generation, $refs will be broken causing runtime errors
			//
			// When force=true, explicit generate=false on nested messages is ignored.
			// This matches the field dependency logic which also forces generation.
			//
			// NOTE: This block is inside `if shouldGen` to ensure nested messages are
			// only forced when the parent is actually generating a schema.
			if len(message.Messages) > 0 {
				nestedResults := gr.getMessagesWithForce(message.Messages, true, true, visited)
				results = append(results, nestedResults...)
			}
		}
	}

	return results
}

// MessageSchemaGenerator handles the generation of JSON Schema code for a single
// Protocol Buffer message. It maintains state during the recursive traversal of
// message fields and nested types.
//
// A new MessageSchemaGenerator is created for each top-level message to ensure
// clean visited state tracking.
type MessageSchemaGenerator struct {
	// gr is a reference to the parent Generator for accessing utility methods
	// like escapeGoString and getTitleAndDescription.
	gr *Generator

	// gen is the output file writer where generated Go code is written.
	// All schema code for this message (and its dependencies) is written here.
	gen *protogen.GeneratedFile

	// visited tracks which messages have already been processed to prevent
	// infinite recursion with circular message references and to avoid
	// generating duplicate schema definitions.
	visited map[string]bool

	// filePrefix is used to generate unique Google type function names when multiple
	// files in the same package import the same Google types. Derived from the proto file name.
	filePrefix string
}

// schemaFieldConfig holds configuration for generating a JSON Schema field.
// It acts as an intermediate representation between the Protocol Buffer field
// descriptor and the generated Go code for the JSON Schema.
//
// This struct supports three main field patterns:
//  1. Scalar fields: typeName is set directly (e.g., "string", "integer")
//  2. Array fields: typeName is "array" with nested config for item schema
//  3. Map fields: typeName is "object" with nested config for additionalProperties
//
// For message-type fields, either messageRef is set (for cross-references to other
// messages) or the nested config contains the inline schema (for Google types).
type schemaFieldConfig struct {
	// fieldName is the field name used in the JSON schema (proto field name in snake_case,
	// or json_name option if explicitly set).
	fieldName string

	// title is the schema title, typically derived from the first paragraph of proto comments.
	title string

	// description is the schema description, derived from proto comments.
	description string

	// typeName is the JSON Schema type (e.g., "string", "object", "array").
	// Empty when using a $ref to another schema.
	typeName string

	// format is the JSON Schema format annotation (e.g., "date-time", "email", "byte").
	format string

	// pattern is a regex pattern for string validation.
	// Used for custom field patterns.
	pattern string

	// propertyNamesPattern is a regex pattern for validating map keys.
	// Used when map keys are integers or booleans (serialized as strings in JSON).
	propertyNamesPattern string

	// enumValues contains the allowed integer values for enum fields.
	enumValues []int32

	// isBytes indicates if the field is a bytes type, requiring base64 contentEncoding.
	isBytes bool

	// messageRef is the Go function call to get a referenced message's schema.
	// Format: "MessageName_JsonSchema_WithDefs(defs)" for same-package messages,
	// or fully qualified for cross-package references.
	messageRef string

	// nested holds the schema configuration for container element types:
	//   - For arrays (repeated fields): describes the Items schema
	//   - For maps: describes the AdditionalProperties schema (map values)
	// This enables recursive schema definitions for nested arrays/maps of messages.
	nested *schemaFieldConfig
}

// -----------------------------------------------------------------------------
// MessageSchemaGenerator Methods
// -----------------------------------------------------------------------------

// emitSchemaField generates Go code for a JSON Schema field definition.
//
// This is the central code generation method that transforms a schemaFieldConfig
// into actual Go source code. It handles all field types (scalars, arrays, maps,
// messages) and applies field-level option overrides.
//
// The generated code structure depends on the field type:
//   - Scalar fields: Direct schema with type and constraints
//   - Array fields: Schema with Items sub-schema
//   - Map fields: Schema with AdditionalProperties sub-schema
//   - Message references: Either direct function call or inline schema for Google types
//
// Options from the proto field definition can override default values for:
//   - Metadata: title, description
//   - Container constraints: minItems, maxItems, uniqueItems, minProperties, maxProperties
//   - Value constraints: format, pattern, contentEncoding, min/max, minLength/maxLength
func (sg *MessageSchemaGenerator) emitSchemaField(cfg schemaFieldConfig, field *protogen.Field) {
	opts := getFieldJsonSchemaOptions(field)
	jsonNumberType := protogen.GoIdent{GoImportPath: "encoding/json", GoName: "Number"}

	// --- Optimization: Direct Message Reference ---
	// If this is a simple message reference with no custom options, we can emit
	// a direct function call instead of creating a new schema object.
	// This produces cleaner generated code like: schema.Properties["user"] = User_JsonSchema_WithDefs(defs)
	{
		if cfg.messageRef != "" && cfg.typeName == "" && cfg.nested == nil {
			if opts == nil {
				sg.gen.P(fmt.Sprintf(`schema.Properties["%s"] = %s`, cfg.fieldName, cfg.messageRef))
				return
			}
		}
	}

	// --- Begin Schema Object ---
	sg.gen.P(fmt.Sprintf(`schema.Properties["%s"] = &jsonschema.Schema{`, cfg.fieldName))

	// Emit type if specified (not set for pure $ref schemas).
	if cfg.typeName != "" {
		sg.gen.P(fmt.Sprintf(`Type: "%s",`, cfg.typeName))
	}

	// --- Metadata Fields ---
	// Title and description from proto comments, with option overrides.
	{
		title := cfg.title
		if opts.GetTitle() != "" {
			title = opts.GetTitle()
		}
		sg.gen.P(fmt.Sprintf(`Title: "%s",`, sg.gr.escapeGoString(title)))

		desc := cfg.description
		if opts.GetDescription() != "" {
			desc = opts.GetDescription()
		}
		sg.gen.P(fmt.Sprintf(`Description: "%s",`, sg.gr.escapeGoString(desc)))
	}

	// --- Container Constraints ---
	// These apply to the root schema for arrays (minItems, maxItems, uniqueItems)
	// and maps (minProperties, maxProperties).
	{
		if opts.GetMinItems() != 0 {
			sg.gen.P(fmt.Sprintf(`MinItems: %d,`, opts.GetMinItems()))
		}
		if opts.GetMaxItems() != 0 {
			sg.gen.P(fmt.Sprintf(`MaxItems: %d,`, opts.GetMaxItems()))
		}
		if opts.GetUniqueItems() {
			sg.gen.P(`UniqueItems: true,`)
		}
		if opts.GetMinProperties() != 0 {
			sg.gen.P(fmt.Sprintf(`MinProperties: %d,`, opts.GetMinProperties()))
		}
		if opts.GetMaxProperties() != 0 {
			sg.gen.P(fmt.Sprintf(`MaxProperties: %d,`, opts.GetMaxProperties()))
		}
	}

	// emitValueConstraints is a closure that generates value-level validation constraints.
	// It's used for both root schemas (scalar fields) and nested schemas (array items, map values).
	// The closure captures 'opts' to allow option overrides at the appropriate level.
	emitValueConstraints := func(c schemaFieldConfig) {
		// --- String Format ---
		// Semantic validation hint (e.g., "date-time", "email", "uri").
		{
			format := c.format
			if opts.GetFormat() != "" {
				format = opts.GetFormat()
			}
			if format != "" {
				sg.gen.P(fmt.Sprintf(`Format: "%s",`, sg.gr.escapeGoString(format)))
			}
		}

		// --- String Pattern ---
		// Regex pattern for string validation.
		{
			pattern := c.pattern
			if opts.GetPattern() != "" {
				pattern = opts.GetPattern()
			}
			if pattern != "" {
				sg.gen.P(fmt.Sprintf(`Pattern: "%s",`, sg.gr.escapeGoString(pattern)))
			}
		}

		// --- Content Encoding ---
		// For binary data (bytes fields), default to base64 unless overridden.
		{
			if opts.GetContentEncoding() != "" {
				sg.gen.P(fmt.Sprintf(`ContentEncoding: "%s",`, sg.gr.escapeGoString(opts.GetContentEncoding())))
			} else if c.isBytes {
				sg.gen.P(`ContentEncoding: "base64",`)
			}
		}

		// --- Content Media Type ---
		// Optional hint about the content's media type (e.g., "application/json").
		if opts.GetContentMediaType() != "" {
			sg.gen.P(fmt.Sprintf(`ContentMediaType: "%s",`, sg.gr.escapeGoString(opts.GetContentMediaType())))
		}

		// --- Numeric Constraints ---
		// Minimum/maximum values with optional exclusive bounds.
		{
			if opts.GetMinimum() != 0 {
				// Use QualifiedGoIdent to ensure encoding/json is imported only when needed.
				sg.gen.P(fmt.Sprintf(`Minimum: %s("%g"),`, sg.gen.QualifiedGoIdent(jsonNumberType), opts.GetMinimum()))
			}
			if opts.GetMaximum() != 0 {
				sg.gen.P(fmt.Sprintf(`Maximum: %s("%g"),`, sg.gen.QualifiedGoIdent(jsonNumberType), opts.GetMaximum()))
			}
			if opts.GetExclusiveMinimum() {
				sg.gen.P(`ExclusiveMinimum: true,`)
			}
			if opts.GetExclusiveMaximum() {
				sg.gen.P(`ExclusiveMaximum: true,`)
			}
		}

		// --- String Length Constraints ---
		{
			if opts.GetMinLength() != 0 {
				sg.gen.P(fmt.Sprintf(`MinLength: %d,`, opts.GetMinLength()))
			}
			if opts.GetMaxLength() != 0 {
				sg.gen.P(fmt.Sprintf(`MaxLength: %d,`, opts.GetMaxLength()))
			}
		}

		// --- Enum Values ---
		// For enum fields, emit the allowed values.
		if len(c.enumValues) > 0 {
			sg.gen.P(`Enum: []any{`)
			for _, enumValue := range c.enumValues {
				sg.gen.P(fmt.Sprintf(`%d,`, enumValue))
			}
			sg.gen.P(`},`)
		}
	}

	// --- Nested Structures (Arrays/Maps) ---
	// For container types, we need to emit the Items or AdditionalProperties schema.
	if cfg.nested != nil {
		// Determine the target field name: Items for arrays, AdditionalProperties for maps.
		targetField := "Items"
		if cfg.typeName == jsObject {
			targetField = "AdditionalProperties"
		}

		if cfg.nested.messageRef != "" {
			// Message reference: emit direct function call for the nested schema.
			sg.gen.P(fmt.Sprintf(`%s: %s,`, targetField, cfg.nested.messageRef))
		} else {
			// Inline schema: emit full schema definition for scalar items, Google types, etc.
			sg.gen.P(fmt.Sprintf(`%s: &jsonschema.Schema{`, targetField))

			// Emit type for the nested schema.
			if cfg.nested.typeName != "" {
				sg.gen.P(fmt.Sprintf(`Type: "%s",`, cfg.nested.typeName))
			} else if cfg.nested.nested == nil {
				// Fallback for external types without explicit type info (e.g., google.type.LatLng).
				sg.gen.P(`Type: "object",`)
			}

			// Apply value constraints to the nested element schema.
			emitValueConstraints(*cfg.nested)

			sg.gen.P(`},`)
		}
	} else {
		// --- Scalar Values ---
		// For non-container types, apply value constraints directly to the root schema.
		emitValueConstraints(cfg)
	}

	// --- Map Property Names ---
	// For maps with non-string keys (integers, booleans), add PropertyNames validation.
	// JSON serializes all map keys as strings, so we use a pattern to validate the format.
	if cfg.propertyNamesPattern != "" {
		sg.gen.P(`PropertyNames: &jsonschema.Schema{`)
		sg.gen.P(fmt.Sprintf(`Pattern: "%s",`, sg.gr.escapeGoString(cfg.propertyNamesPattern)))
		sg.gen.P(`},`)
	}

	sg.gen.P("}")
}

// -----------------------------------------------------------------------------
// Schema Configuration Builders
// -----------------------------------------------------------------------------
//
// These methods create schemaFieldConfig structs for different field types.
// They translate Protocol Buffer field descriptors into the intermediate
// configuration format used by emitSchemaField.

// getArraySchemaConfig creates a schema configuration for repeated (array) fields.
//
// The configuration includes:
//   - Root type: "array"
//   - Nested config: Schema for the array's Items (element type)
//
// Special handling for specific element types:
//   - Messages: References to other message schemas
//   - Enums: Integer type with enum values
//   - Bytes: String type with base64 encoding
func (sg *MessageSchemaGenerator) getArraySchemaConfig(field *protogen.Field, title, description string) schemaFieldConfig {
	kindTypeName, _ := sg.getKindTypeName(field.Desc)

	cfg := schemaFieldConfig{
		fieldName:   getFieldName(field),
		title:       title,
		description: description,
		typeName:    jsArray,
	}

	// Create the nested config based on the element type.
	switch field.Desc.Kind() {
	case protoreflect.MessageKind:
		// Message elements: delegate to getMessageSchemaConfig for Google type handling or reference.
		nestedCfg := sg.getMessageSchemaConfig(field.Message)
		cfg.nested = &nestedCfg

	case protoreflect.EnumKind:
		// Enum elements: integer type with allowed values.
		cfg.nested = &schemaFieldConfig{typeName: kindTypeName, enumValues: sg.getEnumValues(field)}

	case protoreflect.BytesKind:
		// Bytes elements: string type with base64 encoding.
		cfg.nested = &schemaFieldConfig{typeName: kindTypeName, isBytes: true}

	default:
		// All other scalar types (including 64-bit integers): use the direct JSON Schema type mapping.
		cfg.nested = &schemaFieldConfig{typeName: kindTypeName}
	}

	return cfg
}

// getMapSchemaConfig creates a schema configuration for map fields.
//
// Maps in Protocol Buffers are represented as JSON objects where:
//   - Keys become property names (always strings in JSON)
//   - Values become property values (AdditionalProperties schema)
//
// Key handling:
//   - String keys: No special validation needed
//   - Integer keys: PropertyNames pattern validates numeric string format
//   - Boolean keys: PropertyNames pattern validates "true" or "false" strings
//
// Value handling mirrors getArraySchemaConfig for consistency.
func (sg *MessageSchemaGenerator) getMapSchemaConfig(field *protogen.Field, title, description string) schemaFieldConfig {
	cfg := schemaFieldConfig{
		fieldName:   getFieldName(field),
		title:       title,
		description: description,
		typeName:    jsObject,
	}

	mapValue := field.Desc.MapValue()
	mapKey := field.Desc.MapKey()

	// --- Key Validation ---
	// In JSON, all object keys are strings. For non-string proto map keys,
	// we add a PropertyNames pattern to validate the string format.
	{
		switch mapKey.Kind() {
		case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Uint32Kind,
			protoreflect.Fixed32Kind, protoreflect.Sfixed32Kind,
			protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Uint64Kind,
			protoreflect.Fixed64Kind, protoreflect.Sfixed64Kind:
			// Integer keys must be numeric strings.
			cfg.propertyNamesPattern = "^-?[0-9]+$"

		case protoreflect.BoolKind:
			// Boolean keys must be "true" or "false".
			cfg.propertyNamesPattern = "^(true|false)$"
		}
	}

	// --- Value Schema ---
	// Create the nested config for the map's AdditionalProperties (value type).
	kindTypeName, _ := sg.getKindTypeName(mapValue)

	switch mapValue.Kind() {
	case protoreflect.MessageKind:
		// Message values: find the value message from the synthetic map entry.
		// Map fields are represented as repeated synthetic messages with key (field 1)
		// and value (field 2) fields.
		var valMsg *protogen.Message
		for _, f := range field.Message.Fields {
			if f.Desc.Number() == 2 { // Field number 2 is always the value in map entries
				valMsg = f.Message
				break
			}
		}
		if valMsg != nil {
			nestedCfg := sg.getMessageSchemaConfig(valMsg)
			cfg.nested = &nestedCfg
		}

	case protoreflect.EnumKind:
		// Enum values: use descriptor-based enum extraction (no field context available).
		cfg.nested = &schemaFieldConfig{typeName: kindTypeName, enumValues: sg.getEnumValuesFromDescriptor(mapValue.Enum())}

	case protoreflect.BytesKind:
		// Bytes values: string type with base64 encoding.
		cfg.nested = &schemaFieldConfig{typeName: kindTypeName, isBytes: true}

	default:
		// All other scalar types (including 64-bit integers): direct JSON Schema type mapping.
		cfg.nested = &schemaFieldConfig{typeName: kindTypeName}
	}

	return cfg
}

// getScalarSchemaConfig creates a schema configuration for non-repeated, non-map fields.
//
// "Scalar" here includes both primitive types and singular message fields. For message
// fields, it delegates to getMessageSchemaConfig which handles Google types specially.
//
// Type-specific handling:
//   - Messages: Merges config from getMessageSchemaConfig (may be Google type or reference)
//   - Enums: Adds enum value constraints
//   - Bytes: Marks for base64 contentEncoding
//   - Other primitives: Direct type mapping
func (sg *MessageSchemaGenerator) getScalarSchemaConfig(field *protogen.Field, title, description string) schemaFieldConfig {
	kindTypeName, _ := sg.getKindTypeName(field.Desc)

	cfg := schemaFieldConfig{
		fieldName:   getFieldName(field),
		title:       title,
		description: description,
		typeName:    kindTypeName,
	}

	switch field.Desc.Kind() {
	case protoreflect.MessageKind:
		// For message fields, get the config from getMessageSchemaConfig and merge it.
		// This handles Google types (returning inline schemas) and user messages (returning refs).
		nestedCfg := sg.getMessageSchemaConfig(field.Message)
		cfg.typeName = nestedCfg.typeName
		cfg.format = nestedCfg.format
		cfg.pattern = nestedCfg.pattern
		cfg.messageRef = nestedCfg.messageRef
		cfg.nested = nestedCfg.nested
		// Inherit description from message schema if not set on field.
		if cfg.description == "" && nestedCfg.description != "" {
			cfg.description = nestedCfg.description
		}

	case protoreflect.EnumKind:
		// Enum fields: add the allowed integer values.
		cfg.enumValues = sg.getEnumValues(field)

	case protoreflect.BytesKind:
		// Bytes fields: flag for base64 encoding.
		cfg.isBytes = true
	}

	return cfg
}

// getMessageSchemaConfig creates a schema configuration for message-type fields.
//
// All messages (including Google types) are handled as references to schema generation functions.
func (sg *MessageSchemaGenerator) getMessageSchemaConfig(msg *protogen.Message) schemaFieldConfig {
	// Return a reference to the message's schema generation function.
	return schemaFieldConfig{messageRef: sg.referenceName(msg)}
}

// referenceName generates the Go function call expression to retrieve a message's schema.
//
// For same-package messages: "MessageName_JsonSchema_WithDefs(defs)"
// For cross-package messages: "otherpkg.MessageName_JsonSchema_WithDefs(defs)"
// For Google types: "admin_google_protobuf_Timestamp_JsonSchema_WithDefs(defs)" (standalone function with file prefix)
func (sg *MessageSchemaGenerator) referenceName(msg *protogen.Message) string {
	// Check if this is a Google type
	if isGoogleType(msg) {
		// For Google types, use the standalone function name format with file prefix
		funcName := googleTypeFunctionName(msg, sg.filePrefix) + "_JsonSchema_WithDefs"
		return funcName + "(defs)"
	}

	// Build the function identifier with proper import path for cross-package refs.
	funcName := msg.GoIdent.GoName + "_JsonSchema_WithDefs"
	ident := protogen.GoIdent{GoName: funcName, GoImportPath: msg.GoIdent.GoImportPath}

	// QualifiedGoIdent handles import aliasing and returns the properly qualified name.
	return sg.gen.QualifiedGoIdent(ident) + "(defs)"
}

// -----------------------------------------------------------------------------
// Message Schema Generation
// -----------------------------------------------------------------------------

// generateMessageJSONSchema generates the complete JSON Schema code for a single message.
//
// This method produces two Go functions for each message:
//
//  1. JsonSchema() method - Public entry point that returns a complete schema with
//     all definitions bundled. This is the primary API for consumers.
//
//  2. <MessageName>_JsonSchema_WithDefs() function - Internal helper that populates
//     a shared definitions map. This enables cross-references between messages and
//     prevents infinite recursion with circular references.
//
// The generated schema includes:
//   - Type "object" with Properties for each field
//   - Required array for non-optional, non-oneof fields
//   - OneOf/AllOf constraints for proto oneof groups
//   - $ref references to other message schemas in $defs
//
// Schema structure follows JSON Schema Draft 2020-12 using $defs for definitions.
func (sg *MessageSchemaGenerator) generateMessageJSONSchema(message *protogen.Message) error {
	// --- Circular Reference Protection ---
	// Skip if we've already generated this message's schema.
	messageName := string(message.Desc.FullName())
	if sg.visited[messageName] {
		return nil
	}
	sg.visited[messageName] = true

	goName := message.GoIdent.GoName
	title, description := sg.gr.getTitleAndDescription(message.Desc)

	// --- Generate Public Entry Point ---
	// For Google types, generate standalone functions instead of methods (since we can't add methods to imported types).
	// The file prefix ensures unique function names when multiple files in the same package import Google types.
	// Ref-as-root pattern: return a $ref wrapper with full defs. This avoids circular
	// references when marshaling (root != defs[key]) and enables recursive types.
	defKey := string(message.Desc.FullName())
	if isGoogleType(message) {
		googleFuncName := googleTypeFunctionName(message, sg.filePrefix)
		sg.gen.P(fmt.Sprintf("// %s_JsonSchema returns the JSON schema for the %s message.", googleFuncName, message.Desc.Name()))
		sg.gen.P(fmt.Sprintf("func %s_JsonSchema() *jsonschema.Schema {", googleFuncName))
		sg.gen.P("defs := make(map[string]*jsonschema.Schema)")
		sg.gen.P(fmt.Sprintf("_ = %s_JsonSchema_WithDefs(defs)", googleFuncName))
		sg.gen.P(fmt.Sprintf("root := &jsonschema.Schema{Ref: \"#/$defs/%s\"}", defKey))
		sg.gen.P("root.Defs = defs")
		sg.gen.P("return root")
		sg.gen.P("}")
		sg.gen.P()
	} else {
		// Regular messages get methods
		sg.gen.P(fmt.Sprintf("// JsonSchema returns the JSON schema for the %s message.", message.Desc.Name()))
		sg.gen.P(fmt.Sprintf("func (x *%s) JsonSchema() *jsonschema.Schema {", goName))
		sg.gen.P("defs := make(map[string]*jsonschema.Schema)")
		sg.gen.P(fmt.Sprintf("_ = %s_JsonSchema_WithDefs(defs)", goName))
		sg.gen.P(fmt.Sprintf("root := &jsonschema.Schema{Ref: \"#/$defs/%s\"}", defKey))
		sg.gen.P("root.Defs = defs")
		sg.gen.P("return root")
		sg.gen.P("}")
		sg.gen.P()
	}

	// --- Generate Internal Helper ---
	// This function populates the shared definitions map and returns a $ref.
	// The early return on existing defs prevents infinite recursion.
	{
		// Use Google type function name (with file prefix) for Google types, regular Go name for others
		var helperFuncName string
		if isGoogleType(message) {
			helperFuncName = googleTypeFunctionName(message, sg.filePrefix) + "_JsonSchema_WithDefs"
		} else {
			helperFuncName = goName + "_JsonSchema_WithDefs"
		}
		sg.gen.P(fmt.Sprintf("func %s(defs map[string]*jsonschema.Schema) *jsonschema.Schema {", helperFuncName))

		// Return early if already defined (handles circular references).
		sg.gen.P(fmt.Sprintf("if _, ok := defs[\"%s\"]; ok {", defKey))
		sg.gen.P(fmt.Sprintf("return &jsonschema.Schema{Ref: \"#/$defs/%s\"}", defKey))
		sg.gen.P("}")
		sg.gen.P()
	}

	// --- Generate Schema Object ---
	{
		sg.gen.P("schema := &jsonschema.Schema{")
		sg.gen.P(`Type: "object",`)
		if title != "" {
			sg.gen.P(fmt.Sprintf(`Title: "%s",`, sg.gr.escapeGoString(title)))
		}
		if description != "" {
			sg.gen.P(fmt.Sprintf(`Description: "%s",`, sg.gr.escapeGoString(description)))
		}
		sg.gen.P(`Properties: make(map[string]*jsonschema.Schema),`)
	}

	// --- Collect Required Fields ---
	// A field is required only if it's a singular scalar/message field that is not optional.
	// Fields are NOT required if they are: in a oneof, marked optional, repeated (arrays), or maps.
	// Note: In proto3, all singular fields are implicitly optional unless explicitly required.
	var requiredFields []string
	for _, field := range message.Fields {
		opts := getFieldJsonSchemaOptions(field)
		if opts.GetIgnore() {
			continue
		}
		// Fields in oneofs, marked optional, repeated (arrays), or maps are not required.
		if field.Oneof == nil && !field.Desc.HasOptionalKeyword() && !field.Desc.IsList() && !field.Desc.IsMap() {
			requiredFields = append(requiredFields, getFieldName(field))
		}
	}

	// Emit Required array if any fields are required.
	if len(requiredFields) > 0 {
		sg.gen.P(`Required: []string{`)
		for _, f := range requiredFields {
			sg.gen.P(fmt.Sprintf(`"%s",`, f))
		}
		sg.gen.P(`},`)
	}
	sg.gen.P("}")
	sg.gen.P()

	// Register schema in definitions before processing fields to handle self-references.
	sg.gen.P(`// Register schema BEFORE processing fields to handle self-references.`)
	sg.gen.P(`// This prevents infinite recursion when a message contains itself.`)
	sg.gen.P(fmt.Sprintf("defs[\"%s\"] = schema", defKey))
	sg.gen.P()

	// --- Generate Field Schemas and Collect OneOf Groups ---
	// Track oneof groups for generating mutual exclusivity constraints.
	oneofGroups := make(map[string][]string)
	for _, field := range message.Fields {
		opts := getFieldJsonSchemaOptions(field)
		if opts.GetIgnore() {
			continue
		}

		// Track fields that belong to oneof groups (excluding synthetic oneofs for optional).
		if oneof := field.Oneof; oneof != nil && !oneof.Desc.IsSynthetic() {
			groupName := string(oneof.Desc.Name())
			oneofGroups[groupName] = append(oneofGroups[groupName], getFieldName(field))
		}

		// Generate the field's schema.
		if err := sg.generateFieldJSONSchema(field); err != nil {
			return err
		}
		sg.gen.P("")
	}

	// --- Generate OneOf Constraints ---
	// Proto oneof fields are mutually exclusive. In JSON Schema:
	// - Single oneof group: Use OneOf at the schema root
	// - Multiple oneof groups: Use AllOf containing individual OneOf constraints
	if len(oneofGroups) > 0 {
		// Sort group names for deterministic output.
		var groupNames []string
		for name := range oneofGroups {
			groupNames = append(groupNames, name)
		}
		sort.Strings(groupNames)

		if len(groupNames) == 1 {
			// Single oneof: Direct OneOf constraint.
			fields := oneofGroups[groupNames[0]]
			sg.gen.P(`schema.OneOf = []*jsonschema.Schema{`)
			for _, f := range fields {
				sg.gen.P(fmt.Sprintf(`{Required: []string{"%s"}},`, f))
			}
			sg.gen.P(`}`)
		} else {
			// Multiple oneofs: Wrap each in AllOf for independent validation.
			sg.gen.P(`schema.AllOf = []*jsonschema.Schema{`)
			for _, name := range groupNames {
				fields := oneofGroups[name]
				sg.gen.P(`{`)
				sg.gen.P(`OneOf: []*jsonschema.Schema{`)
				for _, f := range fields {
					sg.gen.P(fmt.Sprintf(`{Required: []string{"%s"}},`, f))
				}
				sg.gen.P(`},`)
				sg.gen.P(`},`)
			}
			sg.gen.P(`}`)
		}
	}

	// Return a $ref to this message's schema definition.
	sg.gen.P(fmt.Sprintf("    return &jsonschema.Schema{Ref: \"#/$defs/%s\"}", defKey))
	sg.gen.P("}")
	return nil
}

// generateFieldJSONSchema generates the schema code for a single proto field.
//
// This method acts as a router, determining the field category and delegating
// to the appropriate config builder:
//   - List fields (repeated) → getArraySchemaConfig
//   - Map fields → getMapSchemaConfig
//   - All other fields (singular messages, scalars) → getScalarSchemaConfig
//
// The resulting config is then passed to emitSchemaField for code generation.
func (sg *MessageSchemaGenerator) generateFieldJSONSchema(field *protogen.Field) error {
	// Extract metadata from proto comments.
	title, description := sg.gr.getTitleAndDescription(field.Desc)

	// Route to appropriate config builder based on field cardinality.
	var cfg schemaFieldConfig
	if field.Desc.IsList() {
		cfg = sg.getArraySchemaConfig(field, title, description)
	} else if field.Desc.IsMap() {
		cfg = sg.getMapSchemaConfig(field, title, description)
	} else {
		cfg = sg.getScalarSchemaConfig(field, title, description)
	}

	// Generate the actual schema code.
	sg.emitSchemaField(cfg, field)
	return nil
}

// -----------------------------------------------------------------------------
// Type Mapping Utilities
// -----------------------------------------------------------------------------

// getFieldName returns the proto field name (snake_case) to use in the JSON schema.
// This uses the proto field name directly, not the JSON name, since agents/MCP tools
// use json.Marshal instead of protojson.Marshal.
func getFieldName(field *protogen.Field) string {
	return string(field.Desc.Name())
}

// getKindTypeName maps Protocol Buffer field kinds to JSON Schema type names.
//
// This follows the proto3 JSON mapping specification, with special handling:
//   - bytes → "string" (will be base64 encoded)
//   - enums → "integer" (numeric values for encoding/json compatibility)
//
// Note: The returned type is the base JSON Schema type. Additional constraints
// (patterns, formats, etc.) are added by the caller based on context.
func (sg *MessageSchemaGenerator) getKindTypeName(desc protoreflect.FieldDescriptor) (string, error) {
	switch desc.Kind() {
	case protoreflect.BoolKind:
		return jsBoolean, nil

	case protoreflect.EnumKind:
		// Enums use integer type for encoding/json compatibility (numeric values).
		return jsInteger, nil

	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Uint32Kind, protoreflect.Fixed32Kind, protoreflect.Sfixed32Kind:
		// 32-bit integers fit safely in JavaScript numbers.
		return jsInteger, nil

	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Uint64Kind, protoreflect.Fixed64Kind, protoreflect.Sfixed64Kind:
		// 64-bit integers fit safely in JavaScript numbers.
		return jsInteger, nil

	case protoreflect.FloatKind, protoreflect.DoubleKind:
		// Floating point numbers map directly to JSON numbers.
		return jsNumber, nil

	case protoreflect.StringKind:
		return jsString, nil

	case protoreflect.BytesKind:
		// Bytes are base64-encoded as strings in proto3 JSON.
		return jsString, nil

	case protoreflect.MessageKind:
		// Messages become JSON objects (with properties defined elsewhere).
		return jsObject, nil

	case protoreflect.GroupKind:
		// Groups (deprecated proto2 feature) are treated like messages.
		return jsObject, nil

	default:
		return "", fmt.Errorf("unsupported type: %s", desc.Kind())
	}
}

// escapeGoString prepares a string for embedding in generated Go source code.
// It handles special characters (quotes, newlines, etc.) by using Go's strconv.Quote,
// then strips the outer quotes since the caller will add their own.
//
// Example: "Hello\nWorld" becomes Hello\nWorld (without surrounding quotes).
func (gr *Generator) escapeGoString(s string) string {
	quoted := strconv.Quote(s)
	// Strip the surrounding quotes added by strconv.Quote
	return quoted[1 : len(quoted)-1]
}

// getTitleAndDescription extracts title and description from proto comments.
//
// The parsing follows a convention where:
//   - If comments contain a blank line (paragraph break), the first paragraph becomes
//     the title and the rest becomes the description
//   - If no blank line exists, the entire comment becomes the description (no title)
//
// This allows proto authors to write documentation like:
//
//	// User Profile
//	//
//	// Represents a user's profile information including personal details,
//	// preferences, and account settings.
//	message UserProfile { ... }
//
// Which produces title="User Profile" and description="Represents a user's..."
func (gr *Generator) getTitleAndDescription(desc protoreflect.Descriptor) (title string, description string) {
	// Get the source location information which contains comments.
	src := desc.ParentFile().SourceLocations().ByDescriptor(desc)

	if src.LeadingComments != "" {
		comments := strings.TrimSpace(src.LeadingComments)

		// Try to split on Unix-style blank line first, then Windows-style.
		parts := strings.SplitN(comments, "\n\n", 2)
		if len(parts) < 2 {
			parts = strings.SplitN(comments, "\r\n\r\n", 2)
		}

		// If we found a paragraph break, use first part as title.
		if len(parts) == 2 {
			title = strings.TrimSpace(parts[0])
			description = strings.TrimSpace(parts[1])
		} else {
			// No paragraph break: entire comment is the description.
			description = comments
		}
	}

	return title, description
}

// getEnumValues extracts the list of allowed enum numeric values from a field.
//
// This is used for enum fields where we have access to the full protogen.Field,
// which includes the Enum member with its Values slice.
//
// Returns numeric values (int32) for encoding/json compatibility.
// Example: [0, 1, 2, 3, 4] for UserStatus enum
func (sg *MessageSchemaGenerator) getEnumValues(field *protogen.Field) []int32 {
	var enumValues []int32
	for _, value := range field.Enum.Values {
		enumValues = append(enumValues, int32(value.Desc.Number()))
	}
	return enumValues
}

// getEnumValuesFromDescriptor extracts enum numeric values from a descriptor.
//
// This is used for map value enums where we only have the EnumDescriptor
// (from MapValue().Enum()) rather than a full protogen.Field.
//
// Returns numeric values (int32) for encoding/json compatibility.
func (sg *MessageSchemaGenerator) getEnumValuesFromDescriptor(enumDesc protoreflect.EnumDescriptor) []int32 {
	var enumValues []int32
	values := enumDesc.Values()
	for i := 0; i < values.Len(); i++ {
		enumValues = append(enumValues, int32(values.Get(i).Number()))
	}
	return enumValues
}

// -----------------------------------------------------------------------------
// Proto Options Extraction Helpers
// -----------------------------------------------------------------------------
//
// These functions extract JSON Schema options from Protocol Buffer definitions.
// Options are defined as proto extensions and allow users to customize schema
// generation at the file, message, and field levels.
//
// The option protos are defined in the optionsPb package and include settings like:
//   - generate: Enable/disable schema generation
//   - ignore: Skip specific fields
//   - title, description: Override metadata
//   - Validation constraints: pattern, format, min/max, etc.

// getFileJsonSchemaOptions extracts JSON Schema options from a proto file.
//
// File-level options control default behavior for all messages in the file:
//   - generate: If true, all messages in this file will generate schemas by default
//
// Returns nil if no JSON Schema options are set on the file.
func getFileJsonSchemaOptions(file *protogen.File) *optionsPb.FileOptions_JsonSchema {
	opts := file.Desc.Options()
	if !proto.HasExtension(opts, optionsPb.E_File) {
		return nil
	}
	fileOpts := proto.GetExtension(opts, optionsPb.E_File).(*optionsPb.FileOptions)
	return fileOpts.GetJsonSchema()
}

// getMessageJsonSchemaOptions extracts JSON Schema options from a proto message.
//
// Message-level options override file-level defaults for specific messages:
//   - generate: Enable/disable schema generation for this message
//
// Returns nil if no JSON Schema options are set on the message.
func getMessageJsonSchemaOptions(message *protogen.Message) *optionsPb.MessageOptions_JsonSchema {
	opts := message.Desc.Options()
	if !proto.HasExtension(opts, optionsPb.E_Message) {
		return nil
	}
	msgOpts := proto.GetExtension(opts, optionsPb.E_Message).(*optionsPb.MessageOptions)
	return msgOpts.GetJsonSchema()
}

// getFieldJsonSchemaOptions extracts JSON Schema options from a proto field.
//
// Field-level options provide fine-grained control over individual fields:
//   - ignore: Exclude this field from the schema
//   - title, description: Override metadata from comments
//   - format, pattern: String validation
//   - minimum, maximum: Numeric validation
//   - minLength, maxLength: String length validation
//   - minItems, maxItems, uniqueItems: Array validation
//   - minProperties, maxProperties: Object validation
//   - contentEncoding, contentMediaType: Binary data hints
//
// Returns nil if no JSON Schema options are set on the field.
// Note: Callers should handle nil gracefully; the proto getter methods
// return zero values when called on nil receivers.
func getFieldJsonSchemaOptions(field *protogen.Field) *optionsPb.FieldOptions_JsonSchema {
	opts := field.Desc.Options()
	if !proto.HasExtension(opts, optionsPb.E_Field) {
		return nil
	}
	fieldOpts := proto.GetExtension(opts, optionsPb.E_Field).(*optionsPb.FieldOptions)
	return fieldOpts.GetJsonSchema()
}
