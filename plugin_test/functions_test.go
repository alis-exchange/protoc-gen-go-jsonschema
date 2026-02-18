//go:build plugintest

package plugintest

import (
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
	helper := s.TestingHelper()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"empty string", "", ""},
		{"simple string", "hello world", "hello world"},
		{"string with quotes", `say "hello"`, `say \"hello\"`},
		{"string with newline", "line1\nline2", `line1\nline2`},
		{"string with tab", "col1\tcol2", `col1\tcol2`},
		{"string with backslash", `path\to\file`, `path\\to\\file`},
		{"string with carriage return", "line1\r\nline2", `line1\r\nline2`},
		{"unicode characters", "Hello, 世界", "Hello, 世界"},
		{"mixed special characters", "\"Hello\"\n\tWorld\\!", `\"Hello\"\n\tWorld\\!`},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			result := helper.EscapeGoString(tt.input)
			s.Equal(tt.expected, result, "EscapeGoString(%q)", tt.input)
		})
	}
}

// TestGetKindTypeName tests the type mapping from protobuf kinds to JSON Schema types.
func (s *FunctionsTestSuite) TestGetKindTypeName() {
	helper := s.TestingHelper()

	tests := []struct {
		messageName string
		fieldName   string
		expected    string
	}{
		{"User", "id", jsString},
		{"User", "name", jsString},
		{"User", "status", jsInteger},
		{"ComprehensiveUser", "is_active", jsBoolean},
		{"ComprehensiveUser", "age", jsInteger},
		{"ComprehensiveUser", "user_id", jsInteger},
		{"ComprehensiveUser", "score", jsInteger},
		{"ComprehensiveUser", "account_number", jsInteger},
		{"ComprehensiveUser", "rating", jsNumber},
		{"ComprehensiveUser", "balance", jsNumber},
		{"ComprehensiveUser", "avatar", jsString},
		{"ComprehensiveUser", "address", jsObject},
	}

	for _, tt := range tests {
		s.Run(tt.messageName+"."+tt.fieldName, func() {
			msg := s.FindMessage(tt.messageName)
			field := s.FindField(msg, tt.fieldName)

			typeName, err := helper.GetKindTypeName(field.Desc)
			s.Require().NoError(err, "GetKindTypeName failed")
			s.Equal(tt.expected, typeName, "GetKindTypeName(%s.%s)", tt.messageName, tt.fieldName)
		})
	}
}

// TestGetKindTypeNameAllKinds tests all protoreflect.Kind values.
func (s *FunctionsTestSuite) TestGetKindTypeNameAllKinds() {
	helper := s.TestingHelper()
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

			typeName, err := helper.GetKindTypeName(field.Desc)
			s.Require().NoError(err, "GetKindTypeName failed")
			s.Equal(tt.expectedType, typeName, "GetKindTypeName(%s)", tt.fieldName)
		})
	}
}

// TestGetTitleAndDescription tests comment parsing for title and description.
func (s *FunctionsTestSuite) TestGetTitleAndDescription() {
	helper := s.TestingHelper()

	tests := []struct {
		messageName  string
		expectTitle  bool
		expectDesc   bool
		descContains string
	}{
		{"Address", false, true, "Address represents a physical mailing address"},
		{"ComprehensiveUser", false, true, "ComprehensiveUser is a comprehensive user message"},
		{"User", false, true, "User represents a basic user account"},
	}

	for _, tt := range tests {
		s.Run(tt.messageName, func() {
			msg := s.FindMessage(tt.messageName)
			title, desc := helper.GetTitleAndDescription(msg.Desc)

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
	helper := s.TestingHelper()

	msg := s.FindMessage("User")
	statusField := s.FindField(msg, "status")

	enumValues := helper.GetEnumValues(statusField)
	expectedValues := []int32{0, 1, 2, 3, 4}

	s.Require().Len(enumValues, len(expectedValues), "Expected %d enum values", len(expectedValues))
	for i, expected := range expectedValues {
		s.Equal(expected, enumValues[i], "Enum value %d mismatch", i)
	}
}

// TestGetEnumValuesFromDescriptor tests enum value extraction from a descriptor.
func (s *FunctionsTestSuite) TestGetEnumValuesFromDescriptor() {
	helper := s.TestingHelper()

	msg := s.FindMessage("MapFieldsDemo")
	enumMapField := s.FindField(msg, "string_enum_map")

	mapValue := enumMapField.Desc.MapValue()
	s.Require().Equal(protoreflect.EnumKind, mapValue.Kind(), "Expected enum kind for map value")

	enumValues := helper.GetEnumValuesFromDescriptor(mapValue.Enum())

	s.Require().NotEmpty(enumValues, "Expected enum values")
	s.Equal(int32(0), enumValues[0], "First enum value should be 0 (USER_STATUS_UNSPECIFIED)")
}

// TestGetMessageSchemaConfigGoogleTypes tests Google type handling.
func (s *FunctionsTestSuite) TestGetMessageSchemaConfigGoogleTypes() {
	helper := s.TestingHelper()
	msg := s.FindMessage("WellKnownTypesDemo")

	tests := []struct {
		fieldName        string
		expectedRef      string
		expectMessageRef bool
	}{
		{"created_at", "google_protobuf_Timestamp_JsonSchema_WithDefs(defs)", true},
		{"time_duration", "google_protobuf_Duration_JsonSchema_WithDefs(defs)", true},
		{"struct_field", "google_protobuf_Struct_JsonSchema_WithDefs(defs)", true},
		{"any_field", "google_protobuf_Any_JsonSchema_WithDefs(defs)", true},
	}

	for _, tt := range tests {
		s.Run(tt.fieldName, func() {
			field := s.FindField(msg, tt.fieldName)
			cfg := helper.GetMessageSchemaConfig(field.Message)

			if tt.expectMessageRef {
				s.NotEmpty(cfg.MessageRef, "MessageRef for %s should be set", tt.fieldName)
				s.Contains(cfg.MessageRef, tt.expectedRef, "MessageRef for %s should contain expected function name", tt.fieldName)
				s.Empty(cfg.TypeName, "TypeName for %s should be empty (using $ref)", tt.fieldName)
			}
		})
	}
}

// TestGetScalarSchemaConfig tests scalar field schema configuration.
func (s *FunctionsTestSuite) TestGetScalarSchemaConfig() {
	helper := s.TestingHelper()
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
		{"user_id", jsInteger, false, ""},
		{"rating", jsNumber, false, ""},
		{"avatar", jsString, true, ""},
		{"status", jsInteger, false, ""},
	}

	for _, tt := range tests {
		s.Run(tt.fieldName, func() {
			field := s.FindField(msg, tt.fieldName)
			cfg := helper.GetScalarSchemaConfig(field, "Title", "Description")

			s.Equal(tt.expectedType, cfg.TypeName, "Type for %s", tt.fieldName)
			s.Equal(tt.expectBytes, cfg.IsBytes, "IsBytes for %s", tt.fieldName)
			if tt.expectPattern != "" {
				s.Equal(tt.expectPattern, cfg.Pattern, "Pattern for %s", tt.fieldName)
			}
		})
	}
}

// TestGetArraySchemaConfig tests repeated field schema configuration.
func (s *FunctionsTestSuite) TestGetArraySchemaConfig() {
	helper := s.TestingHelper()
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
		{"long_list", jsInteger, false, "", false, false},
		{"bool_list", jsBoolean, false, "", false, false},
		{"bytes_list", jsString, true, "", false, false},
		{"enum_list", jsInteger, false, "", true, false},
		{"message_list", "", false, "", false, true},
	}

	for _, tt := range tests {
		s.Run(tt.fieldName, func() {
			field := s.FindField(msg, tt.fieldName)
			cfg := helper.GetArraySchemaConfig(field, "Title", "Description")

			s.Equal(jsArray, cfg.TypeName, "Type for %s", tt.fieldName)
			s.Require().NotNil(cfg.Nested, "Expected nested config for %s", tt.fieldName)

			if !tt.hasMessageRef {
				s.Equal(tt.nestedType, cfg.Nested.TypeName, "Nested type for %s", tt.fieldName)
			}
			s.Equal(tt.nestedIsBytes, cfg.Nested.IsBytes, "Nested IsBytes for %s", tt.fieldName)
			if tt.nestedPattern != "" {
				s.Equal(tt.nestedPattern, cfg.Nested.Pattern, "Nested pattern for %s", tt.fieldName)
			}
			if tt.hasEnumValues {
				s.NotEmpty(cfg.Nested.EnumValues, "Expected enum values for %s", tt.fieldName)
			}
			if tt.hasMessageRef {
				s.NotEmpty(cfg.Nested.MessageRef, "Expected message ref for %s", tt.fieldName)
			}
		})
	}
}

// TestGetMapSchemaConfig tests map field schema configuration.
func (s *FunctionsTestSuite) TestGetMapSchemaConfig() {
	helper := s.TestingHelper()
	msg := s.FindMessage("MapFieldsDemo")

	tests := []struct {
		fieldName            string
		propertyNamesPattern string
		nestedType           string
		hasEnumValues        bool
		hasMessageRef        bool
	}{
		{"string_map", "", jsString, false, false},
		{"string_int_map", "", jsInteger, false, false},
		{"string_bool_map", "", jsBoolean, false, false},
		{"string_enum_map", "", jsInteger, true, false},
		{"string_message_map", "", "", false, true},
		{"int_string_map", "^-?[0-9]+$", jsString, false, false},
		{"bool_string_map", "^(true|false)$", jsString, false, false},
	}

	for _, tt := range tests {
		s.Run(tt.fieldName, func() {
			field := s.FindField(msg, tt.fieldName)
			cfg := helper.GetMapSchemaConfig(field, "Title", "Description")

			s.Equal(jsObject, cfg.TypeName, "Type for %s", tt.fieldName)
			s.Equal(tt.propertyNamesPattern, cfg.PropertyNamesPattern, "PropertyNamesPattern for %s", tt.fieldName)
			s.Require().NotNil(cfg.Nested, "Expected nested config for %s", tt.fieldName)

			if !tt.hasMessageRef {
				s.Equal(tt.nestedType, cfg.Nested.TypeName, "Nested type for %s", tt.fieldName)
			}
			if tt.hasEnumValues {
				s.NotEmpty(cfg.Nested.EnumValues, "Expected enum values for %s", tt.fieldName)
			}
			if tt.hasMessageRef {
				s.NotEmpty(cfg.Nested.MessageRef, "Expected message ref for %s", tt.fieldName)
			}
		})
	}
}

// TestSchemaFieldConfigMetadata tests that field metadata is properly captured.
func (s *FunctionsTestSuite) TestSchemaFieldConfigMetadata() {
	helper := s.TestingHelper()

	msg := s.FindMessage("User")
	field := s.FindField(msg, "id")

	cfg := helper.GetScalarSchemaConfig(field, "Custom Title", "Custom Description")

	s.Equal("id", cfg.FieldName, "FieldName mismatch")
	s.Equal("Custom Title", cfg.Title, "Title mismatch")
	s.Equal("Custom Description", cfg.Description, "Description mismatch")
}
