package plugintest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alis-exchange/protoc-gen-go-jsonschema/plugin"
)

// TestOptionsDemoSemantics verifies the new field options and declared oneof
// groups end-to-end: the generated schemas must resolve and enforce
// exactly-one groups, narrowed enums, multipleOf, and carry default/examples.
func (s *IntegrationTestSuite) TestOptionsDemoSemantics() {
	if testing.Short() {
		s.T().Skip("Skipping options demo semantics test in short mode")
	}
	requireProtoc(s.T())

	demoProto := "options_demo/v1/options_demo.proto"
	tmpDir := s.TempDir()
	descPath := filepath.Join(tmpDir, "options_demo.pb")
	fds := buildDescriptorSet(s.T(), s.workspaceRoot, []string{demoProto}, descPath)

	p := createTestPlugin(s.T(), fds, []string{demoProto})
	s.Require().NoError(plugin.Generate(p, "test"), "Generate failed for options demo")

	pkgDir := filepath.Join(tmpDir, "optionsdemov1")
	s.Require().NoError(os.MkdirAll(pkgDir, 0o755))
	for name, content := range getGeneratedContent(s.T(), p) {
		if !strings.HasSuffix(name, "_jsonschema.pb.go") {
			continue
		}
		s.Require().NoError(os.WriteFile(filepath.Join(pkgDir, filepath.Base(name)), []byte(content), 0o644))
	}

	goMod := `module testoptions

go 1.26

require github.com/google/jsonschema-go v0.4.3
`
	s.Require().NoError(os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(goMod), 0o644))

	stubTypes := `package optionsdemov1

type CheckoutRequest struct{}
type Card struct{}
type BankTransfer struct{}
type MobileMoney struct{}
type KeywordShowcase struct{}
type KeywordShowcase_Status int32
type RealOneofDemo struct{}
`
	s.Require().NoError(os.WriteFile(filepath.Join(pkgDir, "stub_types.go"), []byte(stubTypes), 0o644))

	writeSchemaTestHelpers(s.T(), pkgDir, "optionsdemov1")

	testContent := `package optionsdemov1

import (
	"encoding/json"
	"strings"
	"testing"
)

// validShowcase returns a document satisfying every KeywordShowcase constraint.
func validShowcase() map[string]any {
	return map[string]any{
		"weekday": 1, "rate": 0.5, "mode": "compact", "verbose": true,
		"step": 10, "legacy_id": "x", "etag": "e", "status": 1,
		"legacy_card": map[string]any{"expiry_year": 2030},
		"tags":        []any{"a"},
	}
}

func TestExactlyOnePaymentGroup(t *testing.T) {
	resolved, err := ValidateSchemaWithName("CheckoutRequest", (&CheckoutRequest{}).JsonSchema())
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}

	one := map[string]any{"card": map[string]any{"expiry_year": 2030}}
	if err := resolved.Validate(one); err != nil {
		t.Errorf("exactly one payment method should validate: %v", err)
	}

	if err := resolved.Validate(map[string]any{}); err == nil {
		t.Error("no payment method set should fail the required oneof group")
	}

	two := map[string]any{
		"card":          map[string]any{"expiry_year": 2030},
		"bank_transfer": map[string]any{"currency": "USD"},
	}
	if err := resolved.Validate(two); err == nil {
		t.Error("two payment methods set should fail the oneof group")
	}

	// The promo/gift-card group is at-most-one: both absent is fine, both set fails.
	bothCodes := map[string]any{
		"card":       map[string]any{"expiry_year": 2030},
		"promo_code": "p", "gift_card_code": "g",
	}
	if err := resolved.Validate(bothCodes); err == nil {
		t.Error("promo and gift card codes together should fail the at-most-one group")
	}
	promoOnly := map[string]any{
		"card":       map[string]any{"expiry_year": 2030},
		"promo_code": "p",
	}
	if err := resolved.Validate(promoOnly); err != nil {
		t.Errorf("promo code alone should validate: %v", err)
	}
}

func TestKeywordConstraints(t *testing.T) {
	resolved, err := ValidateSchemaWithName("KeywordShowcase", (&KeywordShowcase{}).JsonSchema())
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}

	if err := resolved.Validate(validShowcase()); err != nil {
		t.Fatalf("valid showcase should validate: %v", err)
	}

	invalid := func(key string, value any) {
		doc := validShowcase()
		doc[key] = value
		if err := resolved.Validate(doc); err == nil {
			t.Errorf("%s = %v should fail validation", key, value)
		}
	}
	invalid("step", 7)      // multipleOf 5
	invalid("status", 3)    // enum narrowed to [1, 2]
	invalid("weekday", 9)   // enum_int [0..6]
	invalid("mode", "loud") // enum_string
	invalid("rate", 0.3)    // enum_number
	invalid("tags", []any{"z"})

	// default/examples are annotations: they must appear in the schema JSON.
	raw, err := json.Marshal((&KeywordShowcase{}).JsonSchema())
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	for _, want := range []string{
		"\"default\":\"compact\"",
		"\"default\":1",
		"\"default\":true",
		"\"examples\":[\"compact\"]",
		"\"multipleOf\":5",
		"\"deprecated\":true",
		"\"readOnly\":true",
		"\"writeOnly\":true",
	} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("schema JSON should contain %s", want)
		}
	}
}

func TestRealOneofGroupSemantics(t *testing.T) {
	resolved, err := ValidateSchemaWithName("RealOneofDemo", (&RealOneofDemo{}).JsonSchema())
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}

	if err := resolved.Validate(map[string]any{"Selector": map[string]any{"ById": 5}}); err != nil {
		t.Errorf("one variant set should validate: %v", err)
	}
	if err := resolved.Validate(map[string]any{"Selector": nil}); err == nil {
		t.Error("unset selector should fail the required group")
	}
	if err := resolved.Validate(map[string]any{}); err == nil {
		t.Error("missing selector should fail the required group")
	}
	if err := resolved.Validate(map[string]any{"Selector": map[string]any{"ById": 5, "ByName": "x"}}); err == nil {
		t.Error("two variants set should fail")
	}
}
`
	s.Require().NoError(os.WriteFile(filepath.Join(pkgDir, "options_demo_test.go"), []byte(testContent), 0o644))

	cmd := exec.Command("go", "mod", "tidy")
	cmd.Dir = tmpDir
	output, err := cmd.CombinedOutput()
	s.Require().NoError(err, "go mod tidy failed: %s", string(output))

	cmd = exec.Command("go", "test", "-v", "-timeout", "60s", "./...")
	cmd.Dir = tmpDir
	output, err = cmd.CombinedOutput()
	s.T().Logf("Options demo semantics output:\n%s", string(output))
	s.Require().NoError(err, "options demo semantics tests failed: %s", string(output))
}
