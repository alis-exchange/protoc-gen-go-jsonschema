package plugintest

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

// FunctionsTestSuite pins the shape of generated code for oneofs, message
// references, and Google types. Helper-level unit coverage (type mapping,
// comment parsing, option application) lives in-package in plugin/ against
// the schema model.
type FunctionsTestSuite struct {
	PluginTestSuite
}

// TestFunctionsSuite runs the FunctionsTestSuite.
func TestFunctionsSuite(t *testing.T) {
	suite.Run(t, new(FunctionsTestSuite))
}

// TestOneofGeneratedShape verifies that user-message oneofs generate nested
// PascalCase wrapper properties instead of flat root-level properties.
func (s *FunctionsTestSuite) TestOneofGeneratedShape() {
	content := s.GetGeneratedContent()

	// Extract the ComprehensiveUser function to scope assertions.
	cuSection := extractGoFuncSection(content, "ComprehensiveUser_JsonSchema_WithDefs")
	s.Require().NotEmpty(cuSection, "ComprehensiveUser_JsonSchema_WithDefs not found")

	// Oneof members must NOT appear as flat root properties in ComprehensiveUser.
	s.NotContains(cuSection, `schema.Properties["email"]`,
		"Oneof member 'email' should not be a flat root property")
	s.NotContains(cuSection, `schema.Properties["credit_card"]`,
		"Oneof member 'credit_card' should not be a flat root property")
	s.NotContains(cuSection, `schema.Properties["contact_info"]`,
		"Oneof member 'contact_info' should not be a flat root property")

	// Wrapper properties must use oneof.GoName (PascalCase).
	s.Contains(cuSection, `schema.Properties["Identifier"]`,
		"Oneof wrapper should use PascalCase 'Identifier'")
	s.Contains(cuSection, `schema.Properties["PaymentMethod"]`,
		"Oneof wrapper should use PascalCase 'PaymentMethod'")
	s.Contains(cuSection, `schema.Properties["ContactPreference"]`,
		"Oneof wrapper should use PascalCase 'ContactPreference'")

	// Variant keys must use field.GoName (PascalCase).
	s.Contains(cuSection, `"Email": &jsonschema.Schema{`,
		"Scalar variant key should use PascalCase 'Email'")
	s.Contains(cuSection, `"CreditCard": &jsonschema.Schema{`,
		"Scalar variant key should use PascalCase 'CreditCard'")
	s.Contains(cuSection, `"UserNumber": &jsonschema.Schema{`,
		"Scalar variant key should use PascalCase 'UserNumber'")

	// Message variants call the target's _WithDefs (bare, or decorated when
	// the field carries metadata/options); the call inlines or $refs as the
	// target decides.
	s.Contains(cuSection, `ContactInfo_JsonSchema_WithDefs(defs)`,
		"Message variant should call the target's _WithDefs")
	s.NotContains(cuSection, `"ContactInfo": &jsonschema.Schema{`,
		"Message variant should not emit an inline schema")
	s.Contains(cuSection, `Address_JsonSchema_WithDefs(defs)`,
		"Message variant should call the target's _WithDefs")
	s.NotContains(cuSection, `"MailingAddress": &jsonschema.Schema{`,
		"Message variant should not emit an inline schema")

	// No root-level AllOf constraints for ComprehensiveUser.
	s.NotContains(cuSection, `schema.AllOf`,
		"User messages should not have root-level AllOf constraints")
}

// TestOneofWrapperStructure verifies each oneof wrapper has the correct
// OneOf branches with object type and Required.
func (s *FunctionsTestSuite) TestOneofWrapperStructure() {
	content := s.GetGeneratedContent()

	// OneOfDemo uses Field1, Field2, Field3, Field4 as wrapper names.
	s.Contains(content, `schema.Properties["Field1"]`,
		"OneOfDemo should have PascalCase wrapper 'Field1'")
	s.Contains(content, `schema.Properties["Field2"]`,
		"OneOfDemo should have PascalCase wrapper 'Field2'")
	s.Contains(content, `schema.Properties["Field3"]`,
		"OneOfDemo should have PascalCase wrapper 'Field3'")
	s.Contains(content, `schema.Properties["Field4"]`,
		"OneOfDemo should have PascalCase wrapper 'Field4'")

	// Each branch should have Required
	s.Contains(content, `Required: []string{"StringValue"}`,
		"Branch should have Required with PascalCase variant key")
	s.Contains(content, `Required: []string{"IntValue"}`,
		"Branch should have Required with PascalCase variant key")

	// Single-member oneof (AddressDetails.address_details) still wraps.
	s.Contains(content, `schema.Properties["AddressDetails"]`,
		"Single-member oneof should still have wrapper property")
	s.Contains(content, `Required: []string{"OneofAddressDetails"}`,
		"Single-member oneof should still have Required")
}

// TestOneofWrapperAllowsNull verifies that oneof wrapper properties accept null
// (the unset-oneof case) via a leading null branch, plus an object branch holding
// the variant oneOf. encoding/json always emits the wrapper key, with value null
// when the oneof is unset.
func (s *FunctionsTestSuite) TestOneofWrapperAllowsNull() {
	content := s.GetGeneratedContent()

	cuSection := extractGoFuncSection(content, "ComprehensiveUser_JsonSchema_WithDefs")
	s.Require().NotEmpty(cuSection)

	// The Identifier wrapper should expose a null branch and an object branch.
	identIdx := strings.Index(cuSection, `schema.Properties["Identifier"]`)
	s.Require().Greater(identIdx, 0)
	afterIdent := cuSection[identIdx : identIdx+200]
	s.Contains(afterIdent, `{Type: "null"},`,
		"Oneof wrapper should accept null for the unset-oneof case")
	s.Contains(afterIdent, `Type: "object"`,
		"Oneof wrapper should still have an object branch for the variants")
}

// TestMessageFieldOptionsEmitAsRefSiblings verifies that a message-type field
// with field-level json_schema options keeps its _WithDefs call and applies
// the options to the returned schema (overrides on an inline copy, Draft
// 2020-12 siblings on a $ref).
func (s *FunctionsTestSuite) TestMessageFieldOptionsEmitAsRefSiblings() {
	content := s.GetGeneratedContent()

	// ConstraintDemo.shipping_address has a description override: the
	// _WithDefs call stays, and the description is set on the fresh schema
	// the call returns.
	s.Contains(content, `schema.Properties["shipping_address"] = Address_JsonSchema_WithDefs(defs)`,
		"Message field with options should still call _WithDefs")
	s.Contains(content, `schema.Properties["shipping_address"].Description = `,
		"Message field options should decorate the returned schema")

	// It should NOT emit an inline &jsonschema.Schema{ for shipping_address.
	s.NotContains(content, `schema.Properties["shipping_address"] = &jsonschema.Schema{`,
		"Message field with options should not emit a literal schema (would lose the target structure)")
}

// TestOneofMessageVariantOptionsEmitAsRefSiblings verifies that a message-type
// oneof variant with field-level json_schema options keeps its _WithDefs call
// and gains the options via a decorating closure.
func (s *FunctionsTestSuite) TestOneofMessageVariantOptionsEmitAsRefSiblings() {
	content := s.GetGeneratedContent()
	cuSection := extractGoFuncSection(content, "ComprehensiveUser_JsonSchema_WithDefs")
	s.Require().NotEmpty(cuSection)

	// contact_info has a description override: the variant decorates the
	// returned schema inside a closure.
	s.Contains(cuSection, `s := ContactInfo_JsonSchema_WithDefs(defs)`,
		"Oneof message variant with options should still call _WithDefs")
	s.Contains(cuSection, `s.Description = `,
		"Oneof message variant options should decorate the returned schema")
	s.NotContains(cuSection, `"ContactInfo": &jsonschema.Schema{`,
		"Oneof message variant with options should not emit a literal schema (would lose the target structure)")
}

func (s *FunctionsTestSuite) TestGoogleTypeOneofUsesWrapper() {
	contents := s.RunGenerate()

	// common_jsonschema.pb.go references google.iam.admin.v1.LintPolicyRequest,
	// whose proto oneof 'lint_object' marshals with a PascalCase wrapper under
	// encoding/json (no custom json.Marshaler), exactly like user messages.
	var commonContent string
	for name, content := range contents {
		if strings.HasSuffix(name, "common_jsonschema.pb.go") {
			commonContent = content
			break
		}
	}
	s.Require().NotEmpty(commonContent, "Expected common_jsonschema.pb.go to be generated")

	lint := extractGoFuncSection(commonContent, "common_google_iam_admin_v1_LintPolicyRequest_JsonSchema_WithDefs")
	s.Require().NotEmpty(lint, "Expected a standalone LintPolicyRequest function")
	s.Contains(lint, `schema.Properties["LintObject"]`,
		"Google type oneof should become a PascalCase wrapper property")
	s.Contains(lint, `"Condition":`,
		"Wrapper should hold the PascalCase variant key")
	s.NotContains(lint, `schema.Properties["condition"]`,
		"Google type oneof member must not stay a flat property")
	s.NotContains(lint, `schema.OneOf = []*jsonschema.Schema{`,
		"Google type must not emit root-level oneof constraints")
}
