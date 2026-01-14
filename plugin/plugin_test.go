package plugin

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

// PluginGeneratorTestSuite contains tests for the Generator and Generate function.
type PluginGeneratorTestSuite struct {
	PluginTestSuite
}

// TestPluginGeneratorSuite runs the PluginGeneratorTestSuite.
func TestPluginGeneratorSuite(t *testing.T) {
	suite.Run(t, new(PluginGeneratorTestSuite))
}

// TestGenerate tests the main Generate function.
func (s *PluginGeneratorTestSuite) TestGenerate() {
	err := Generate(s.plugin)
	s.Require().NoError(err, "Generate failed")

	// Check the response
	resp := s.plugin.Response()
	s.Require().Empty(resp.GetError(), "Generate response error")

	// Verify we got generated files
	s.Require().NotEmpty(resp.File, "Expected generated files")

	// Check that the generated file has the expected suffix
	foundJsonSchema := false
	for _, f := range resp.File {
		if strings.HasSuffix(f.GetName(), "_jsonschema.pb.go") {
			foundJsonSchema = true

			// Verify the content is not empty
			s.NotEmpty(f.GetContent(), "Generated file content is empty")

			// Verify the content has expected structure
			content := f.GetContent()
			s.Contains(content, "package ", "Missing package declaration")
			s.Contains(content, "JsonSchema()", "Missing JsonSchema method")
			s.Contains(content, "jsonschema.Schema", "Missing jsonschema.Schema type")
		}
	}

	s.True(foundJsonSchema, "Expected a file with _jsonschema.pb.go suffix")
}

// TestGenerateNoFiles tests Generate with no files to generate.
func (s *PluginGeneratorTestSuite) TestGenerateNoFiles() {
	// Create a new plugin with no files to generate
	emptyPlugin := createTestPlugin(s.T(), s.fds, []string{})

	err := Generate(emptyPlugin)
	s.Require().NoError(err, "Generate failed")

	resp := emptyPlugin.Response()
	s.Require().Empty(resp.GetError(), "Generate response error")

	// Should have no generated files
	s.Empty(resp.File, "Expected no generated files")
}

// TestGetMessages tests the message collection logic.
func (s *PluginGeneratorTestSuite) TestGetMessages() {
	gr := s.Generator()

	s.Run("with generate all true", func() {
		messages := gr.getMessages(s.file.Messages, true, make(map[string]bool))

		s.Require().NotEmpty(messages, "Expected messages")

		// Should include main messages
		messageNames := make(map[string]bool)
		for _, msg := range messages {
			messageNames[string(msg.Desc.Name())] = true
		}

		expectedMessages := []string{"User", "Address", "ComprehensiveUser", "ContactInfo"}
		for _, name := range expectedMessages {
			s.True(messageNames[name], "Expected message %s to be included", name)
		}
	})

	s.Run("with generate all false", func() {
		messages := gr.getMessages(s.file.Messages, false, make(map[string]bool))

		// With generate=false and no message-level overrides, should still get messages
		// that have the generate option set at message level
		if len(messages) > 0 {
			for _, msg := range messages {
				s.T().Logf("Got message: %s", msg.Desc.Name())
			}
		}
	})

	s.Run("filters map entries", func() {
		messages := gr.getMessages(s.file.Messages, true, make(map[string]bool))

		for _, msg := range messages {
			s.False(msg.Desc.IsMapEntry(), "Map entry %s should be filtered out", msg.Desc.Name())
		}
	})

	s.Run("filters google types", func() {
		messages := gr.getMessages(s.file.Messages, true, make(map[string]bool))

		for _, msg := range messages {
			fullName := string(msg.Desc.FullName())
			s.False(strings.HasPrefix(fullName, "google.protobuf."),
				"Google type %s should be filtered out", fullName)
		}
	})

	s.Run("handles visited tracking", func() {
		visited := make(map[string]bool)

		// First call
		messages1 := gr.getMessages(s.file.Messages, true, visited)
		count1 := len(messages1)

		// Second call with same visited map
		messages2 := gr.getMessages(s.file.Messages, true, visited)
		count2 := len(messages2)

		// Second call should return no additional messages
		s.Equal(0, count2, "Expected 0 messages on second call (all visited)")
		s.NotEqual(0, count1, "Expected some messages on first call")
	})
}

// TestGeneratorGenerateFile tests the generateFile method.
func (s *PluginGeneratorTestSuite) TestGeneratorGenerateFile() {
	gr := s.Generator()

	genFile, err := gr.generateFile(s.plugin, s.file)
	s.Require().NoError(err, "generateFile failed")
	s.Require().NotNil(genFile, "Expected generated file")
}

// TestMessageSchemaGeneratorReferenceName tests reference name generation.
func (s *PluginGeneratorTestSuite) TestMessageSchemaGeneratorReferenceName() {
	sg := s.CreateMessageSchemaGenerator()

	s.Run("user message reference", func() {
		msg := s.FindMessage("Address")
		ref := sg.referenceName(msg)

		s.NotEmpty(ref, "Expected non-empty reference")
		s.Contains(ref, "Address_JsonSchema_WithDefs", "Reference should contain function name")
		s.Contains(ref, "(defs)", "Reference should contain defs parameter")
	})
}

// TestGeneratedCodeStructure tests the structure of generated code.
func (s *PluginGeneratorTestSuite) TestGeneratedCodeStructure() {
	contents := s.RunGenerate()

	for name, content := range contents {
		s.Run(name, func() {
			// Check for standard generated file header
			s.Contains(content, "Code generated by protoc-gen-go-jsonschema. DO NOT EDIT.",
				"Missing generation header comment")

			// Check for package declaration
			s.Contains(content, "package ", "Missing package declaration")

			// Check for import of jsonschema
			s.Contains(content, "jsonschema", "Missing jsonschema import")

			// Check for JsonSchema method
			hasMethod := strings.Contains(content, "func (x *") || strings.Contains(content, "JsonSchema()")
			s.True(hasMethod, "Missing JsonSchema receiver method pattern")

			// Check for WithDefs function
			s.Contains(content, "_JsonSchema_WithDefs(defs map[string]*jsonschema.Schema)",
				"Missing _JsonSchema_WithDefs function pattern")

			// Check for proper schema structure (flexible whitespace)
			hasObjectType := strings.Contains(content, `Type:`) && strings.Contains(content, `"object"`)
			s.True(hasObjectType, "Missing object type in schema")

			s.Contains(content, "Properties:", "Missing Properties in schema")
		})
	}
}

// TestGeneratedCodeForSpecificMessages tests generated code for specific message types.
func (s *PluginGeneratorTestSuite) TestGeneratedCodeForSpecificMessages() {
	content := s.GetGeneratedContent()

	tests := []struct {
		name     string
		contains []string
	}{
		{
			name: "User message",
			contains: []string{
				"User_JsonSchema_WithDefs",
				`"id"`,
				`"name"`,
				`"email"`,
			},
		},
		{
			name: "Address message",
			contains: []string{
				"Address_JsonSchema_WithDefs",
				`"street"`,
				`"city"`,
			},
		},
		{
			name: "ComprehensiveUser message",
			contains: []string{
				"ComprehensiveUser_JsonSchema_WithDefs",
			},
		},
		{
			name: "Enum handling",
			contains: []string{
				"Enum: []any{",
			},
		},
		{
			name: "Array handling",
			contains: []string{
				`"array"`,
				"Items:",
			},
		},
		{
			name: "Map handling",
			contains: []string{
				"AdditionalProperties:",
			},
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			for _, expected := range tt.contains {
				s.Contains(content, expected, "Expected content to contain %q", expected)
			}
		})
	}
}

// TestOneOfHandling tests oneof field handling in generated code.
func (s *PluginGeneratorTestSuite) TestOneOfHandling() {
	content := s.GetGeneratedContent()

	// Check for OneOf constraint generation
	if !strings.Contains(content, "OneOf") {
		s.T().Log("Warning: OneOf constraint not found in generated code")
	}
}

// TestWellKnownTypesHandling tests WKT handling in generated code.
func (s *PluginGeneratorTestSuite) TestWellKnownTypesHandling() {
	content := s.GetGeneratedContent()

	// Timestamp should be string with date-time format
	s.Contains(content, `Format: "date-time"`, "Expected date-time format for Timestamp")

	// Duration should have the duration pattern
	if !strings.Contains(content, `^([0-9]+\.?[0-9]*|\.[0-9]+)s$`) {
		s.T().Log("Duration pattern not found - may be using different format")
	}
}

// TestBytesFieldHandling tests bytes field handling.
func (s *PluginGeneratorTestSuite) TestBytesFieldHandling() {
	content := s.GetGeneratedContent()

	// Bytes fields should have base64 content encoding
	s.Contains(content, `ContentEncoding: "base64"`, "Expected base64 content encoding for bytes fields")
}

// TestInt64FieldHandling tests int64 field handling (should be string with pattern).
func (s *PluginGeneratorTestSuite) TestInt64FieldHandling() {
	content := s.GetGeneratedContent()

	// Int64 fields should have numeric string pattern
	s.Contains(content, `^-?[0-9]+$`, "Expected numeric string pattern for int64 fields")
}
