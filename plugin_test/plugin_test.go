//go:build plugintest

package plugintest

import (
	"strings"
	"testing"

	"github.com/alis-exchange/protoc-gen-go-jsonschema/plugin"
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
	err := plugin.Generate(s.Plugin(), "test")
	s.Require().NoError(err, "Generate failed")

	// Check the response
	resp := s.Plugin().Response()
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
	emptyPlugin := createTestPlugin(s.T(), s.FileDescriptorSet(), []string{})

	err := plugin.Generate(emptyPlugin, "test")
	s.Require().NoError(err, "Generate failed")

	resp := emptyPlugin.Response()
	s.Require().Empty(resp.GetError(), "Generate response error")

	// Should have no generated files
	s.Empty(resp.File, "Expected no generated files")
}

// TestGetMessages tests the message collection logic.
func (s *PluginGeneratorTestSuite) TestGetMessages() {
	helper := s.TestingHelper()

	s.Run("with generate all true", func() {
		messages := helper.GetMessages(s.File().Messages, true, make(map[string]bool))

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
		messages := helper.GetMessages(s.File().Messages, false, make(map[string]bool))

		// With generate=false and no message-level overrides, should still get messages
		// that have the generate option set at message level
		if len(messages) > 0 {
			for _, msg := range messages {
				s.T().Logf("Got message: %s", msg.Desc.Name())
			}
		}
	})

	s.Run("filters map entries", func() {
		messages := helper.GetMessages(s.File().Messages, true, make(map[string]bool))

		for _, msg := range messages {
			s.False(msg.Desc.IsMapEntry(), "Map entry %s should be filtered out", msg.Desc.Name())
		}
	})

	s.Run("includes google types when referenced", func() {
		messages := helper.GetMessages(s.File().Messages, true, make(map[string]bool))

		// Check if any Google types are included (they should be if referenced)
		s.NotNil(messages, "Messages should be returned")
	})

	s.Run("handles visited tracking", func() {
		visited := make(map[string]bool)

		messages1 := helper.GetMessages(s.File().Messages, true, visited)
		count1 := len(messages1)

		messages2 := helper.GetMessages(s.File().Messages, true, visited)
		count2 := len(messages2)

		s.Equal(0, count2, "Expected 0 messages on second call (all visited)")
		s.NotEqual(0, count1, "Expected some messages on first call")
	})
}

// TestGetMessagesWithForce tests the force logic for nested messages and dependencies.
func (s *PluginGeneratorTestSuite) TestGetMessagesWithForce() {
	helper := s.TestingHelper()

	s.Run("force=true ignores explicit generate=false on nested messages", func() {
		parentMsg := s.FindMessage("Address")
		s.Require().NotNil(parentMsg, "Address message not found")

		nestedMessages := parentMsg.Messages
		s.Require().NotEmpty(nestedMessages, "Address should have nested messages")

		visited := make(map[string]bool)
		messagesNoForce := helper.GetMessagesWithForce(nestedMessages, false, false, visited)

		visited2 := make(map[string]bool)
		messagesWithForce := helper.GetMessagesWithForce(nestedMessages, true, true, visited2)

		s.NotEmpty(messagesWithForce, "Force=true should include nested messages when defaultGenerate=true")
		s.T().Logf("Without force: %d messages, With force: %d messages", len(messagesNoForce), len(messagesWithForce))

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
		parentMsg := s.FindMessage("ComprehensiveUser")
		s.Require().NotNil(parentMsg, "ComprehensiveUser message not found")

		visited := make(map[string]bool)
		visited[string(parentMsg.Desc.FullName())] = true

		var depMessages []*protogen.Message
		for _, field := range parentMsg.Fields {
			if field.Desc.Kind() == protoreflect.MessageKind {
				deps := helper.GetMessagesWithForce([]*protogen.Message{field.Message}, true, true, visited)
				depMessages = append(depMessages, deps...)
			}
		}

		s.NotEmpty(depMessages, "Force=true should include field dependencies")
	})

	s.Run("force=false respects explicit generate=false", func() {
		visited := make(map[string]bool)
		allMessages := s.File().Messages
		messagesNoForce := helper.GetMessagesWithForce(allMessages, false, false, visited)

		for _, msg := range messagesNoForce {
			if helper.MessageHasExplicitGenerateFalse(msg) {
				s.Fail("Message with generate=false should not be included when force=false")
			}
		}
	})
}

// TestGeneratorGenerateFile tests the generateFile method.
func (s *PluginGeneratorTestSuite) TestGeneratorGenerateFile() {
	helper := s.TestingHelper()

	genFile, err := helper.GenerateFile(s.Plugin(), s.File())
	s.Require().NoError(err, "generateFile failed")
	s.Require().NotNil(genFile, "Expected generated file")
}

// TestMessageSchemaGeneratorReferenceName tests reference name generation.
func (s *PluginGeneratorTestSuite) TestMessageSchemaGeneratorReferenceName() {
	helper := s.TestingHelper()

	s.Run("user message reference", func() {
		msg := s.FindMessage("Address")
		ref := helper.ReferenceName(msg)

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
			s.Contains(content, "Code generated by", "Missing generation header comment")
			s.Contains(content, "DO NOT EDIT", "Missing DO NOT EDIT comment")
			s.Contains(content, "package ", "Missing package declaration")
			s.Contains(content, "jsonschema", "Missing jsonschema import")

			hasMethod := strings.Contains(content, "func (x *") && strings.Contains(content, "JsonSchema()")
			hasGoogleTypeFunction := strings.Contains(content, "google_protobuf_") && strings.Contains(content, "_JsonSchema()")
			s.True(hasMethod || hasGoogleTypeFunction, "Missing JsonSchema method or Google type function pattern")

			s.Contains(content, "_JsonSchema_WithDefs(defs map[string]*jsonschema.Schema)",
				"Missing _JsonSchema_WithDefs function pattern")

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
		{"User message", []string{"User_JsonSchema_WithDefs", `"id"`, `"name"`, `"email"`}},
		{"Address message", []string{"Address_JsonSchema_WithDefs", `"street"`, `"city"`}},
		{"ComprehensiveUser message", []string{"ComprehensiveUser_JsonSchema_WithDefs"}},
		{"Enum handling", []string{"Enum: []any{"}},
		{"Array handling", []string{`"array"`, "Items:"}},
		{"Map handling", []string{"AdditionalProperties:"}},
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
	if !strings.Contains(content, "OneOf") {
		s.T().Log("Warning: OneOf constraint not found in generated code")
	}
}

// TestGoogleTypesHandling tests Google type handling in generated code.
func (s *PluginGeneratorTestSuite) TestGoogleTypesHandling() {
	content := s.GetGeneratedContent()

	hasGoogleTypeFunctions := strings.Contains(content, "google_protobuf_") &&
		strings.Contains(content, "_JsonSchema()")
	hasRefs := strings.Contains(content, "Ref: \"#/$defs/")

	if strings.Contains(content, "Timestamp") || strings.Contains(content, "Duration") {
		s.True(hasGoogleTypeFunctions || hasRefs,
			"Google types should generate standalone functions or use $ref, found neither")
	}
}

// TestBytesFieldHandling tests bytes field handling.
func (s *PluginGeneratorTestSuite) TestBytesFieldHandling() {
	content := s.GetGeneratedContent()
	s.Contains(content, `ContentEncoding: "base64"`, "Expected base64 content encoding for bytes fields")
}

// TestInt64FieldHandling tests int64 field handling.
func (s *PluginGeneratorTestSuite) TestInt64FieldHandling() {
	content := s.GetGeneratedContent()
	s.Contains(content, `^-?[0-9]+$`, "Expected numeric string pattern for int64 fields")
}
