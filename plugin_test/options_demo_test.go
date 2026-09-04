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
type AnnotationShowcase struct{}
type ElementShowcase struct{}
type DecorationShowcase struct{}
type SubsetOneofDemo struct{}
`
	s.Require().NoError(os.WriteFile(filepath.Join(pkgDir, "stub_types.go"), []byte(stubTypes), 0o644))

	writeSchemaTestHelpers(s.T(), pkgDir, "optionsdemov1")

	testContent := `package optionsdemov1

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

// intPtr reads a *int keyword, or -1 when unset.
func intPtr(p *int) int {
	if p == nil {
		return -1
	}
	return *p
}

// floatPtr reads a *float64 keyword, or NaN-ish sentinel when unset.
func floatPtr(p *float64) float64 {
	if p == nil {
		return -999
	}
	return *p
}

// variant finds the branch of a PascalCase oneof wrapper that holds the
// given variant key, or nil.
func variant(wrapper *jsonschema.Schema, key string) *jsonschema.Schema {
	if wrapper == nil || len(wrapper.OneOf) != 2 {
		return nil
	}
	for _, branch := range wrapper.OneOf[1].OneOf {
		if v, ok := branch.Properties[key]; ok {
			return v
		}
	}
	return nil
}

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

// TestAnnotationOptions pins the annotation-style options on singular fields:
// they must land on the field's schema, and the assertions among them
// (lengths, exclusive bound) must be enforced.
func TestAnnotationOptions(t *testing.T) {
	schema := (&AnnotationShowcase{}).JsonSchema()
	resolved, err := ValidateSchemaWithName("AnnotationShowcase", schema)
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	props := schema.Properties

	if got := props["display_name"]; got.Title != "Display name" || got.Description != "Shown in the UI; overrides the comment." {
		t.Errorf("title/description options must override the comment, got title=%q description=%q", got.Title, got.Description)
	}
	if got := props["email"]; got.Format != "email" || intPtr(got.MinLength) != 3 || intPtr(got.MaxLength) != 254 {
		t.Errorf("email: format/minLength/maxLength wrong: %+v", got)
	}
	if got := props["payload"]; got.ContentEncoding != "base64" || got.ContentMediaType != "application/json" {
		t.Errorf("payload: contentEncoding/contentMediaType wrong: %+v", got)
	}
	if got := props["digest"]; got.ContentEncoding != "base16" || got.ContentMediaType != "application/octet-stream" {
		t.Errorf("digest: bytes default encoding must be overridable: %+v", got)
	}
	if _, ok := props["internal_note"]; ok {
		t.Error("ignored field must not be a property")
	}
	for _, r := range schema.Required {
		if r == "internal_note" {
			t.Error("ignored field must not be required")
		}
	}
	if got := props["ratio"]; got.Maximum != nil || floatPtr(got.ExclusiveMaximum) != 1 {
		t.Errorf("ratio: exclusive_maximum must replace maximum: %+v", got)
	}

	valid := map[string]any{"display_name": "n", "email": "a@b", "payload": "e30=", "digest": "00ff", "ratio": 0.5}
	if err := resolved.Validate(valid); err != nil {
		t.Fatalf("valid document rejected: %v", err)
	}
	invalid := func(key string, value any) {
		doc := map[string]any{}
		for k, v := range valid {
			doc[k] = v
		}
		doc[key] = value
		if err := resolved.Validate(doc); err == nil {
			t.Errorf("%s = %v should fail validation", key, value)
		}
	}
	invalid("email", "ab") // minLength 3
	invalid("ratio", 1)    // exclusiveMaximum 1
}

// TestElementOptions pins where options land on repeated and map fields:
// value constraints on items / additionalProperties, container constraints
// and annotations on the array or object itself.
func TestElementOptions(t *testing.T) {
	schema := (&ElementShowcase{}).JsonSchema()
	resolved, err := ValidateSchemaWithName("ElementShowcase", schema)
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	props := schema.Properties

	slugs := props["slugs"]
	if intPtr(slugs.MinItems) != 1 || intPtr(slugs.MaxItems) != 3 || !slugs.UniqueItems {
		t.Errorf("slugs: container constraints must sit on the array: %+v", slugs)
	}
	if slugs.Items == nil || slugs.Items.Pattern != "^[a-z][a-z0-9-]*$" || intPtr(slugs.Items.MinLength) != 1 || intPtr(slugs.Items.MaxLength) != 16 {
		t.Errorf("slugs: value constraints must sit on items: %+v", slugs.Items)
	}
	quotas := props["quotas"]
	if intPtr(quotas.MinProperties) != 1 || intPtr(quotas.MaxProperties) != 4 {
		t.Errorf("quotas: property bounds must sit on the object: %+v", quotas)
	}
	if v := quotas.AdditionalProperties; v == nil || floatPtr(v.Minimum) != 1 || floatPtr(v.Maximum) != 100 || floatPtr(v.MultipleOf) != 5 {
		t.Errorf("quotas: numeric constraints must sit on additionalProperties: %+v", quotas.AdditionalProperties)
	}
	contacts := props["contacts"]
	if contacts.Title != "Contacts by role" || contacts.AdditionalProperties == nil || contacts.AdditionalProperties.Format != "email" {
		t.Errorf("contacts: title on the map, format on the values: %+v", contacts)
	}
	region := props["region_status"]
	if !region.ReadOnly || region.AdditionalProperties == nil || fmt.Sprint(region.AdditionalProperties.Enum) != "[1 2]" {
		t.Errorf("region_status: read_only on the map, narrowed enum on the values: %+v", region)
	}
	attachments := props["attachments"]
	if !attachments.WriteOnly || attachments.Items == nil || attachments.Items.ContentEncoding != "base16" || attachments.Items.ContentMediaType != "image/png" {
		t.Errorf("attachments: write_only on the array, content keywords on items: %+v", attachments)
	}
	ratios := props["ratios"]
	if ratios.Items == nil || ratios.Items.Minimum != nil || floatPtr(ratios.Items.ExclusiveMinimum) != 0 || floatPtr(ratios.Items.Maximum) != 1 {
		t.Errorf("ratios: exclusive bound must sit on items: %+v", ratios.Items)
	}

	valid := map[string]any{
		"slugs":         []any{"a", "b-1"},
		"quotas":        map[string]any{"x": 5, "y": 100},
		"contacts":      map[string]any{"owner": "a@example.com"},
		"region_status": map[string]any{"eu": 1},
		"attachments":   []any{"00ff"},
		"ratios":        []any{0.5, 1},
	}
	if err := resolved.Validate(valid); err != nil {
		t.Fatalf("valid document rejected: %v", err)
	}
	invalid := func(key string, value any) {
		doc := map[string]any{}
		for k, v := range valid {
			doc[k] = v
		}
		doc[key] = value
		if err := resolved.Validate(doc); err == nil {
			t.Errorf("%s = %v should fail validation", key, value)
		}
	}
	invalid("slugs", []any{"A"})                       // pattern on items
	invalid("slugs", []any{"a", "a"})                  // uniqueItems
	invalid("slugs", []any{})                          // minItems
	invalid("slugs", []any{"a", "b", "c", "d"})        // maxItems
	invalid("slugs", []any{strings.Repeat("a", 17)})   // maxLength on items
	invalid("quotas", map[string]any{})                // minProperties
	invalid("quotas", map[string]any{"x": 7})          // multipleOf on values
	invalid("quotas", map[string]any{"x": 500})        // maximum on values
	invalid("region_status", map[string]any{"eu": 0}) // enum narrowed to [1, 2]
	invalid("ratios", []any{0})                        // exclusiveMinimum on items
	invalid("ratios", []any{2})                        // maximum on items
}

// TestDecorationOptions pins options on message-typed fields (they decorate
// the inline copy) and on oneof variants (they land inside the wrapper).
func TestDecorationOptions(t *testing.T) {
	schema := (&DecorationShowcase{}).JsonSchema()
	resolved, err := ValidateSchemaWithName("DecorationShowcase", schema)
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}

	card := schema.Properties["primary_card"]
	if card == nil || card.Ref != "" || card.Type != "object" || card.Properties["expiry_year"] == nil {
		t.Fatalf("primary_card must be an inline copy of Card, got %+v", card)
	}
	// The field's own comment and title option replace Card's own pair.
	if card.Title != "Primary card" || !strings.HasPrefix(card.Description, "Card decorated with a title") {
		t.Errorf("field title option and comment must replace Card's own title and description, got title=%q description=%q", card.Title, card.Description)
	}
	if intPtr(card.MinProperties) != 1 || !card.ReadOnly {
		t.Errorf("container constraint and annotation must decorate the inline copy: %+v", card)
	}

	wrapper := schema.Properties["Lookup"]
	if byID := variant(wrapper, "ById"); byID == nil || floatPtr(byID.Minimum) != 1 || string(byID.Default) != "1" || fmt.Sprint(byID.Examples) != "[1 42]" {
		t.Errorf("ById: bound, default and examples must sit on the variant: %+v", byID)
	}
	if bySlug := variant(wrapper, "BySlug"); bySlug == nil || bySlug.Pattern != "^[a-z-]+$" || bySlug.Title != "Slug" {
		t.Errorf("BySlug: pattern and title must sit on the variant: %+v", bySlug)
	}
	if byBank := variant(wrapper, "ByBank"); byBank == nil || byBank.Ref != "" || byBank.Properties["currency"] == nil ||
		byBank.Description != "Deprecated lookup by settlement details." || !byBank.Deprecated {
		t.Errorf("ByBank: description and deprecated must decorate the inline message variant: %+v", byBank)
	}
	if variant(wrapper, "ByLegacyKey") != nil {
		t.Error("an ignored oneof member must not appear in the wrapper")
	}

	valid := map[string]any{"primary_card": map[string]any{"expiry_year": 2030}, "Lookup": map[string]any{"ById": 5}}
	if err := resolved.Validate(valid); err != nil {
		t.Fatalf("valid document rejected: %v", err)
	}
	for name, doc := range map[string]map[string]any{
		"minimum on the ById variant":     {"primary_card": map[string]any{"expiry_year": 2030}, "Lookup": map[string]any{"ById": 0}},
		"pattern on the BySlug variant":   {"primary_card": map[string]any{"expiry_year": 2030}, "Lookup": map[string]any{"BySlug": "Bad Slug"}},
		"ignored variant is unknown":      {"primary_card": map[string]any{"expiry_year": 2030}, "Lookup": map[string]any{"ByLegacyKey": "x"}},
		"min_properties on the inline card": {"primary_card": map[string]any{}, "Lookup": nil},
	} {
		if err := resolved.Validate(doc); err == nil {
			t.Errorf("%s: document should fail validation", name)
		}
	}
}

// TestSubsetOneofGroup pins a required json_schema.oneof group covering only
// some members of a proto oneof: the uncovered variant fails the schema.
func TestSubsetOneofGroup(t *testing.T) {
	resolved, err := ValidateSchemaWithName("SubsetOneofDemo", (&SubsetOneofDemo{}).JsonSchema())
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if err := resolved.Validate(map[string]any{"Contact": map[string]any{"ByEmail": "a@b"}}); err != nil {
		t.Errorf("covered variant should validate: %v", err)
	}
	if err := resolved.Validate(map[string]any{"Contact": map[string]any{"ByPager": "1"}}); err == nil {
		t.Error("the uncovered variant should fail the required group")
	}
	if err := resolved.Validate(map[string]any{"Contact": nil}); err == nil {
		t.Error("unset contact should fail the required group")
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
