package plugin

import (
	optionsPb "go.alis.build/common/alis/open/options"
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
)

// This file extracts JSON Schema options from Protocol Buffer definitions.
// Options are defined as proto extensions and allow users to customize schema
// generation at the file, message, and field levels.

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
