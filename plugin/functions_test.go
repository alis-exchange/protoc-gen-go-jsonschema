package plugin

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// FunctionsTestSuite contains tests for the functions in functions.go.
type FunctionsTestSuite struct {
	PluginTestSuite
}

// TestFunctionsSuite runs the FunctionsTestSuite.
func TestFunctionsSuite(t *testing.T) {
	suite.Run(t, new(FunctionsTestSuite))
}

// TestEscapeGoString tests the escapeGoString utility function.
func (s *FunctionsTestSuite) TestEscapeGoString() {
	gr := &Generator{}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "simple string",
			input:    "hello world",
			expected: "hello world",
		},
		{
			name:     "string with quotes",
			input:    `say "hello"`,
			expected: `say \"hello\"`,
		},
		{
			name:     "string with newline",
			input:    "line1\nline2",
			expected: `line1\nline2`,
		},
		{
			name:     "string with tab",
			input:    "col1\tcol2",
			expected: `col1\tcol2`,
		},
		{
			name:     "string with backslash",
			input:    `path\to\file`,
			expected: `path\\to\\file`,
		},
		{
			name:     "string with carriage return",
			input:    "line1\r\nline2",
			expected: `line1\r\nline2`,
		},
		{
			name:     "unicode characters",
			input:    "Hello, 世界",
			expected: "Hello, 世界",
		},
		{
			name:     "mixed special characters",
			input:    "\"Hello\"\n\tWorld\\!",
			expected: `\"Hello\"\n\tWorld\\!`,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			result := gr.escapeGoString(tt.input)
			s.Equal(tt.expected, result, "escapeGoString(%q)", tt.input)
		})
	}
}

// TestGetKindTypeName tests the type mapping from protobuf kinds to JSON Schema types.
func (s *FunctionsTestSuite) TestGetKindTypeName() {
	// Create a MessageSchemaGenerator for testing
	sg := &MessageSchemaGenerator{
		gr:      &Generator{},
		visited: make(map[string]bool),
	}

	tests := []struct {
		messageName string
		fieldName   string
		expected    string
	}{
		// Basic types from User message
		{"User", "id", jsString},
		{"User", "name", jsString},
		{"User", "status", jsInteger}, // enum

		// Comprehensive types from ComprehensiveUser
		{"ComprehensiveUser", "is_active", jsBoolean},
		{"ComprehensiveUser", "age", jsInteger},            // int32
		{"ComprehensiveUser", "user_id", jsInteger},        // int64
		{"ComprehensiveUser", "score", jsInteger},          // uint32
		{"ComprehensiveUser", "account_number", jsInteger}, // uint64
		{"ComprehensiveUser", "rating", jsNumber},          // float
		{"ComprehensiveUser", "balance", jsNumber},         // double
		{"ComprehensiveUser", "avatar", jsString},          // bytes
		{"ComprehensiveUser", "address", jsObject},         // message
	}

	for _, tt := range tests {
		s.Run(tt.messageName+"."+tt.fieldName, func() {
			msg := s.FindMessage(tt.messageName)
			field := s.FindField(msg, tt.fieldName)

			typeName, err := sg.getKindTypeName(field.Desc)
			s.Require().NoError(err, "getKindTypeName failed")
			s.Equal(tt.expected, typeName, "getKindTypeName(%s.%s)", tt.messageName, tt.fieldName)
		})
	}
}

// TestGetKindTypeNameAllKinds tests all protoreflect.Kind values.
func (s *FunctionsTestSuite) TestGetKindTypeNameAllKinds() {
	sg := &MessageSchemaGenerator{
		gr:      &Generator{},
		visited: make(map[string]bool),
	}

	// Test using ComprehensiveUser which has many field types
	msg := s.FindMessage("ComprehensiveUser")

	kindTests := []struct {
		fieldName    string
		expectedKind protoreflect.Kind
		expectedType string
	}{
		{"is_active", protoreflect.BoolKind, jsBoolean},
		{"age", protoreflect.Int32Kind, jsInteger},
		{"user_id", protoreflect.Int64Kind, jsInteger},
		{"score", protoreflect.Uint32Kind, jsInteger},
		{"account_number", protoreflect.Uint64Kind, jsInteger},
		{"signed_score", protoreflect.Sint32Kind, jsInteger},
		{"signed_id", protoreflect.Sint64Kind, jsInteger},
		{"fixed_uint", protoreflect.Fixed32Kind, jsInteger},
		{"fixed_ulong", protoreflect.Fixed64Kind, jsInteger},
		{"sfixed_int", protoreflect.Sfixed32Kind, jsInteger},
		{"sfixed_long", protoreflect.Sfixed64Kind, jsInteger},
		{"rating", protoreflect.FloatKind, jsNumber},
		{"balance", protoreflect.DoubleKind, jsNumber},
		{"id", protoreflect.StringKind, jsString},
		{"avatar", protoreflect.BytesKind, jsString},
		{"status", protoreflect.EnumKind, jsInteger},
		{"address", protoreflect.MessageKind, jsObject},
	}

	for _, tt := range kindTests {
		s.Run(tt.fieldName, func() {
			field := s.FindField(msg, tt.fieldName)

			s.Equal(tt.expectedKind, field.Desc.Kind(), "Field %s kind mismatch", tt.fieldName)

			typeName, err := sg.getKindTypeName(field.Desc)
			s.Require().NoError(err, "getKindTypeName failed")
			s.Equal(tt.expectedType, typeName, "getKindTypeName(%s)", tt.fieldName)
		})
	}
}

// TestGetTitleAndDescription tests comment parsing for title and description.
func (s *FunctionsTestSuite) TestGetTitleAndDescription() {
	gr := &Generator{}

	tests := []struct {
		messageName  string
		expectTitle  bool
		expectDesc   bool
		descContains string
	}{
		{
			messageName:  "Address",
			expectTitle:  false,
			expectDesc:   true,
			descContains: "Address represents a physical mailing address",
		},
		{
			messageName:  "ComprehensiveUser",
			expectTitle:  false,
			expectDesc:   true,
			descContains: "ComprehensiveUser is a comprehensive user message",
		},
		{
			messageName:  "User",
			expectTitle:  false,
			expectDesc:   true,
			descContains: "User represents a basic user account",
		},
	}

	for _, tt := range tests {
		s.Run(tt.messageName, func() {
			msg := s.FindMessage(tt.messageName)
			title, desc := gr.getTitleAndDescription(msg.Desc)

			if tt.expectTitle {
				s.NotEmpty(title, "Expected title for %s", tt.messageName)
			}
			if !tt.expectTitle && title != "" {
				s.T().Logf("Got unexpected title for %s: %q", tt.messageName, title)
			}

			if tt.expectDesc {
				s.NotEmpty(desc, "Expected description for %s", tt.messageName)
			}
			if tt.descContains != "" && desc != "" {
				s.Contains(desc, tt.descContains, "Description for %s should contain expected text", tt.messageName)
			}
		})
	}
}

// TestGetEnumValues tests enum value extraction.
func (s *FunctionsTestSuite) TestGetEnumValues() {
	sg := &MessageSchemaGenerator{
		gr:      &Generator{},
		visited: make(map[string]bool),
	}

	// Test UserStatus enum via the status field in User
	msg := s.FindMessage("User")
	statusField := s.FindField(msg, "status")

	enumValues := sg.getEnumValues(statusField)

	expectedValues := []int32{0, 1, 2, 3, 4} // UserStatus: UNSPECIFIED, ACTIVE, INACTIVE, SUSPENDED, DELETED

	s.Require().Len(enumValues, len(expectedValues), "Expected %d enum values", len(expectedValues))

	for i, expected := range expectedValues {
		s.Equal(expected, enumValues[i], "Enum value %d mismatch", i)
	}
}

// TestGetEnumValuesFromDescriptor tests enum value extraction from a descriptor.
func (s *FunctionsTestSuite) TestGetEnumValuesFromDescriptor() {
	sg := &MessageSchemaGenerator{
		gr:      &Generator{},
		visited: make(map[string]bool),
	}

	// Test using MapFieldsDemo which has enum map values
	msg := s.FindMessage("MapFieldsDemo")
	enumMapField := s.FindField(msg, "string_enum_map")

	// Get the map value's enum descriptor
	mapValue := enumMapField.Desc.MapValue()
	s.Require().Equal(protoreflect.EnumKind, mapValue.Kind(), "Expected enum kind for map value")

	enumValues := sg.getEnumValuesFromDescriptor(mapValue.Enum())

	s.Require().NotEmpty(enumValues, "Expected enum values")
	s.Equal(int32(0), enumValues[0], "First enum value should be 0 (USER_STATUS_UNSPECIFIED)")
}

// TestGetMessageSchemaConfigGoogleTypes tests Google type handling.
func (s *FunctionsTestSuite) TestGetMessageSchemaConfigGoogleTypes() {
	sg := &MessageSchemaGenerator{
		gr:      &Generator{},
		visited: make(map[string]bool),
	}

	// Find WellKnownTypesDemo message
	msg := s.FindMessage("WellKnownTypesDemo")

	tests := []struct {
		fieldName        string
		expectedRef      string
		expectMessageRef bool
	}{
		{"created_at", "google_protobuf_Timestamp_JsonSchema_WithDefs(defs)", true},   // Timestamp
		{"time_duration", "google_protobuf_Duration_JsonSchema_WithDefs(defs)", true}, // Duration
		{"struct_field", "google_protobuf_Struct_JsonSchema_WithDefs(defs)", true},    // Struct
		{"any_field", "google_protobuf_Any_JsonSchema_WithDefs(defs)", true},          // Any
	}

	for _, tt := range tests {
		s.Run(tt.fieldName, func() {
			field := s.FindField(msg, tt.fieldName)
			cfg := sg.getMessageSchemaConfig(field.Message)

			if tt.expectMessageRef {
				s.NotEmpty(cfg.messageRef, "MessageRef for %s should be set", tt.fieldName)
				s.Contains(cfg.messageRef, tt.expectedRef, "MessageRef for %s should contain expected function name", tt.fieldName)
				s.Empty(cfg.typeName, "TypeName for %s should be empty (using $ref)", tt.fieldName)
			}
		})
	}
}

// TestGetScalarSchemaConfig tests scalar field schema configuration.
func (s *FunctionsTestSuite) TestGetScalarSchemaConfig() {
	sg := &MessageSchemaGenerator{
		gr:      &Generator{},
		visited: make(map[string]bool),
	}

	msg := s.FindMessage("ComprehensiveUser")

	tests := []struct {
		fieldName     string
		expectedType  string
		expectBytes   bool
		expectPattern string
	}{
		{"id", jsString, false, ""},
		{"is_active", jsBoolean, false, ""},
		{"age", jsInteger, false, ""},
		{"user_id", jsInteger, false, ""}, // int64
		{"rating", jsNumber, false, ""},
		{"avatar", jsString, true, ""},  // bytes
		{"status", jsInteger, false, ""}, // enum
	}

	for _, tt := range tests {
		s.Run(tt.fieldName, func() {
			field := s.FindField(msg, tt.fieldName)
			cfg := sg.getScalarSchemaConfig(field, "Title", "Description")

			s.Equal(tt.expectedType, cfg.typeName, "Type for %s", tt.fieldName)
			s.Equal(tt.expectBytes, cfg.isBytes, "isBytes for %s", tt.fieldName)
			if tt.expectPattern != "" {
				s.Equal(tt.expectPattern, cfg.pattern, "Pattern for %s", tt.fieldName)
			}
		})
	}
}

// TestGetArraySchemaConfig tests repeated field schema configuration.
func (s *FunctionsTestSuite) TestGetArraySchemaConfig() {
	// Create a MessageSchemaGenerator with gen for message reference tests
	sg := s.CreateMessageSchemaGenerator()

	msg := s.FindMessage("RepeatedFieldsDemo")

	tests := []struct {
		fieldName     string
		nestedType    string
		nestedIsBytes bool
		nestedPattern string
		hasEnumValues bool
		hasMessageRef bool
	}{
		{"string_list", jsString, false, "", false, false},
		{"int_list", jsInteger, false, "", false, false},
		{"long_list", jsInteger, false, "", false, false}, // int64
		{"bool_list", jsBoolean, false, "", false, false},
		{"bytes_list", jsString, true, "", false, false},
		{"enum_list", jsInteger, false, "", true, false},
		{"message_list", "", false, "", false, true}, // Address message
	}

	for _, tt := range tests {
		s.Run(tt.fieldName, func() {
			field := s.FindField(msg, tt.fieldName)
			cfg := sg.getArraySchemaConfig(field, "Title", "Description")

			s.Equal(jsArray, cfg.typeName, "Type for %s", tt.fieldName)
			s.Require().NotNil(cfg.nested, "Expected nested config for %s", tt.fieldName)

			if !tt.hasMessageRef {
				s.Equal(tt.nestedType, cfg.nested.typeName, "Nested type for %s", tt.fieldName)
			}
			s.Equal(tt.nestedIsBytes, cfg.nested.isBytes, "Nested isBytes for %s", tt.fieldName)
			if tt.nestedPattern != "" {
				s.Equal(tt.nestedPattern, cfg.nested.pattern, "Nested pattern for %s", tt.fieldName)
			}
			if tt.hasEnumValues {
				s.NotEmpty(cfg.nested.enumValues, "Expected enum values for %s", tt.fieldName)
			}
			if tt.hasMessageRef {
				s.NotEmpty(cfg.nested.messageRef, "Expected message ref for %s", tt.fieldName)
			}
		})
	}
}

// TestGetMapSchemaConfig tests map field schema configuration.
func (s *FunctionsTestSuite) TestGetMapSchemaConfig() {
	// Create a MessageSchemaGenerator with gen for message reference tests
	sg := s.CreateMessageSchemaGenerator()

	msg := s.FindMessage("MapFieldsDemo")

	tests := []struct {
		fieldName            string
		propertyNamesPattern string
		nestedType           string
		hasEnumValues        bool
		hasMessageRef        bool
	}{
		{"string_map", "", jsString, false, false},                    // map<string, string>
		{"string_int_map", "", jsInteger, false, false},               // map<string, int32>
		{"string_bool_map", "", jsBoolean, false, false},              // map<string, bool>
		{"string_enum_map", "", jsInteger, true, false},               // map<string, UserStatus>
		{"string_message_map", "", "", false, true},                   // map<string, Address>
		{"int_string_map", "^-?[0-9]+$", jsString, false, false},      // map<int32, string>
		{"bool_string_map", "^(true|false)$", jsString, false, false}, // map<bool, string>
	}

	for _, tt := range tests {
		s.Run(tt.fieldName, func() {
			field := s.FindField(msg, tt.fieldName)
			cfg := sg.getMapSchemaConfig(field, "Title", "Description")

			s.Equal(jsObject, cfg.typeName, "Type for %s", tt.fieldName)
			s.Equal(tt.propertyNamesPattern, cfg.propertyNamesPattern, "PropertyNamesPattern for %s", tt.fieldName)
			s.Require().NotNil(cfg.nested, "Expected nested config for %s", tt.fieldName)

			if !tt.hasMessageRef {
				s.Equal(tt.nestedType, cfg.nested.typeName, "Nested type for %s", tt.fieldName)
			}
			if tt.hasEnumValues {
				s.NotEmpty(cfg.nested.enumValues, "Expected enum values for %s", tt.fieldName)
			}
			if tt.hasMessageRef {
				s.NotEmpty(cfg.nested.messageRef, "Expected message ref for %s", tt.fieldName)
			}
		})
	}
}

// TestSchemaFieldConfigMetadata tests that field metadata is properly captured.
func (s *FunctionsTestSuite) TestSchemaFieldConfigMetadata() {
	sg := &MessageSchemaGenerator{
		gr:      &Generator{},
		visited: make(map[string]bool),
	}

	msg := s.FindMessage("User")
	field := s.FindField(msg, "id")

	cfg := sg.getScalarSchemaConfig(field, "Custom Title", "Custom Description")

	s.Equal("id", cfg.fieldName, "fieldName mismatch")
	s.Equal("Custom Title", cfg.title, "title mismatch")
	s.Equal("Custom Description", cfg.description, "description mismatch")
}

// containsString is a helper function to check if a string contains a substring.
func containsString(s, substr string) bool {
	return strings.Contains(s, substr)
}
