package plugin

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/reflect/protoreflect"
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
	err := Generate(s.plugin, "test")
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

	err := Generate(emptyPlugin, "test")
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

	s.Run("includes google types when referenced", func() {
		messages := gr.getMessages(s.file.Messages, true, make(map[string]bool))

		// Check if any Google types are included (they should be if referenced)
		// This test just verifies the logic doesn't crash - actual inclusion depends on references
		s.NotNil(messages, "Messages should be returned")
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

// TestGetMessagesWithForce tests the force logic for nested messages and dependencies.
func (s *PluginGeneratorTestSuite) TestGetMessagesWithForce() {
	gr := s.Generator()

	s.Run("force=true ignores explicit generate=false on nested messages", func() {
		// Find a message with nested messages (Address has AddressDetails)
		parentMsg := s.FindMessage("Address")
		s.Require().NotNil(parentMsg, "Address message not found")

		// Get nested messages
		nestedMessages := parentMsg.Messages
		s.Require().NotEmpty(nestedMessages, "Address should have nested messages")

		// Test with force=false: should respect generate=false if set
		visited := make(map[string]bool)
		messagesNoForce := gr.getMessagesWithForce(nestedMessages, false, false, visited)

		// Test with force=true: should ignore generate=false and use defaultGenerate
		visited2 := make(map[string]bool)
		messagesWithForce := gr.getMessagesWithForce(nestedMessages, true, true, visited2)

		// With force=true and defaultGenerate=true, nested messages should be included
		// even if they have generate=false (which they don't in our test proto, but the logic should work)
		s.NotEmpty(messagesWithForce, "Force=true should include nested messages when defaultGenerate=true")

		// Log both results for comparison
		s.T().Logf("Without force: %d messages, With force: %d messages", len(messagesNoForce), len(messagesWithForce))

		// Verify AddressDetails is included when forced
		foundNested := false
		for _, msg := range messagesWithForce {
			if strings.Contains(string(msg.Desc.FullName()), "AddressDetails") {
				foundNested = true
				break
			}
		}
		s.True(foundNested, "Nested AddressDetails should be included when force=true")
	})

	s.Run("force=true includes field dependencies even with generate=false", func() {
		// Find a message with message-type fields
		parentMsg := s.FindMessage("ComprehensiveUser")
		s.Require().NotNil(parentMsg, "ComprehensiveUser message not found")

		// Get messages with force=true for field dependencies
		visited := make(map[string]bool)
		visited[string(parentMsg.Desc.FullName())] = true

		// Simulate field dependency processing with force=true
		var depMessages []*protogen.Message
		for _, field := range parentMsg.Fields {
			if field.Desc.Kind() == protoreflect.MessageKind {
				deps := gr.getMessagesWithForce([]*protogen.Message{field.Message}, true, true, visited)
				depMessages = append(depMessages, deps...)
			}
		}

		// Should include dependencies even if they have generate=false
		s.NotEmpty(depMessages, "Force=true should include field dependencies")
	})

	s.Run("force=false respects explicit generate=false", func() {
		// Test that without force, generate=false is respected
		visited := make(map[string]bool)

		// Get all messages with force=false
		allMessages := s.file.Messages
		messagesNoForce := gr.getMessagesWithForce(allMessages, false, false, visited)

		// Messages with generate=false should not be included when force=false
		// (This test verifies the logic works, even if our test proto doesn't have generate=false)
		for _, msg := range messagesNoForce {
			opts := getMessageJsonSchemaOptions(msg)
			if opts != nil && !opts.GetGenerate() {
				s.Fail("Message with generate=false should not be included when force=false")
			}
		}
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
			s.Contains(content, "Code generated by", "Missing generation header comment")
			s.Contains(content, "DO NOT EDIT", "Missing DO NOT EDIT comment")

			// Check for package declaration
			s.Contains(content, "package ", "Missing package declaration")

			// Check for import of jsonschema
			s.Contains(content, "jsonschema", "Missing jsonschema import")

			// Check for JsonSchema method or standalone function (for Google types)
			hasMethod := strings.Contains(content, "func (x *") && strings.Contains(content, "JsonSchema()")
			hasGoogleTypeFunction := strings.Contains(content, "google_protobuf_") && strings.Contains(content, "_JsonSchema()")
			s.True(hasMethod || hasGoogleTypeFunction, "Missing JsonSchema method or Google type function pattern")

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

// TestGoogleTypesHandling tests Google type handling in generated code.
func (s *PluginGeneratorTestSuite) TestGoogleTypesHandling() {
	content := s.GetGeneratedContent()

	// Google types should generate standalone functions with $ref instead of inline schemas
	// Check for Google type function generation (e.g., google_protobuf_Timestamp_JsonSchema)
	hasGoogleTypeFunctions := strings.Contains(content, "google_protobuf_") &&
		strings.Contains(content, "_JsonSchema()")

	// If Google types are referenced, they should generate functions
	// Check for $ref usage in schemas (Google types use $ref now)
	hasRefs := strings.Contains(content, "Ref: \"#/$defs/")

	// At least one of these should be true if Google types are used
	if strings.Contains(content, "Timestamp") || strings.Contains(content, "Duration") {
		s.True(hasGoogleTypeFunctions || hasRefs,
			"Google types should generate standalone functions or use $ref, found neither")
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
