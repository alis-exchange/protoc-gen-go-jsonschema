package plugin

import (
	"strings"
	"testing"

	optionsPb "go.alis.build/common/alis/open/options"
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/pluginpb"
)

// dynFile compiles an in-memory proto file holding the given messages and
// returns its protogen form. Message-typed fields reference siblings by
// ".dyn.v1.<Name>"; google/protobuf/struct.proto is always importable.
func dynFile(t *testing.T, msgs ...*descriptorpb.DescriptorProto) *protogen.File {
	t.Helper()
	return dynFileWithEnum(t, msgs...)
}

// dynFileWithEnum is dynFile plus a Color enum (values 0 and 1) for
// enum-typed fields.
func dynFileWithEnum(t *testing.T, msgs ...*descriptorpb.DescriptorProto) *protogen.File {
	t.Helper()
	return dynFileCustom(t, "dyn/v1/dyn.proto", "proto3", msgs...)
}

// dynFileCustom is dynFileWithEnum with a chosen file path and syntax
// ("proto2" allows group fields).
func dynFileCustom(t *testing.T, path, syntax string, msgs ...*descriptorpb.DescriptorProto) *protogen.File {
	t.Helper()
	return findFile(t, dynPluginCustom(t, path, syntax, msgs...), path)
}

// dynPluginCustom compiles the in-memory file (package dyn.v1, with the Color
// enum) and returns the whole plugin, for tests that run Generate.
// google/protobuf/struct.proto and timestamp.proto are importable.
func dynPluginCustom(t *testing.T, path, syntax string, msgs ...*descriptorpb.DescriptorProto) *protogen.Plugin {
	t.Helper()
	structFile := protodesc.ToFileDescriptorProto(structpb.File_google_protobuf_struct_proto)
	timestampFile := protodesc.ToFileDescriptorProto(timestamppb.File_google_protobuf_timestamp_proto)
	fdp := &descriptorpb.FileDescriptorProto{
		Name:        proto.String(path),
		Package:     proto.String("dyn.v1"),
		Syntax:      proto.String(syntax),
		Dependency:  []string{structFile.GetName(), timestampFile.GetName()},
		Options:     &descriptorpb.FileOptions{GoPackage: proto.String("example.com/dyn/v1;dynv1")},
		MessageType: msgs,
		EnumType: []*descriptorpb.EnumDescriptorProto{{
			Name: proto.String("Color"),
			Value: []*descriptorpb.EnumValueDescriptorProto{
				{Name: proto.String("COLOR_UNSPECIFIED"), Number: proto.Int32(0)},
				{Name: proto.String("COLOR_RED"), Number: proto.Int32(1)},
			},
		}},
	}
	req := &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{path},
		ProtoFile:      []*descriptorpb.FileDescriptorProto{structFile, timestampFile, fdp},
	}
	p, err := protogen.Options{}.New(req)
	if err != nil {
		t.Fatalf("Failed to build dynamic plugin: %v", err)
	}
	return p
}

// dynGroupField builds a proto2 group field. The group's message is nested in
// the parent under the capitalised field name, as protoc lays it out;
// groupType is that nested name ("Parent.Grp").
func dynGroupField(name string, number int32, groupType string) *descriptorpb.FieldDescriptorProto {
	return &descriptorpb.FieldDescriptorProto{
		Name:     proto.String(name),
		Number:   proto.Int32(number),
		Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		Type:     descriptorpb.FieldDescriptorProto_TYPE_GROUP.Enum(),
		TypeName: proto.String(".dyn.v1." + groupType),
		JsonName: proto.String(name),
	}
}

// dynIgnored marks a field ignore: true.
func dynIgnored(f *descriptorpb.FieldDescriptorProto) *descriptorpb.FieldDescriptorProto {
	f.Options = &descriptorpb.FieldOptions{}
	proto.SetExtension(f.Options, optionsPb.E_Field, &optionsPb.FieldOptions{
		JsonSchema: &optionsPb.FieldOptions_JsonSchema{Ignore: proto.Bool(true)},
	})
	return f
}

// dynMsgField builds a singular message-typed field referencing target: a
// sibling name ("A") or a fully-qualified name (".google.protobuf.Struct").
func dynMsgField(name string, number int32, target string) *descriptorpb.FieldDescriptorProto {
	if !strings.HasPrefix(target, ".") {
		target = ".dyn.v1." + target
	}
	return &descriptorpb.FieldDescriptorProto{
		Name:     proto.String(name),
		Number:   proto.Int32(number),
		Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
		TypeName: proto.String(target),
		JsonName: proto.String(name),
	}
}

// dynRepeated marks a field repeated.
func dynRepeated(f *descriptorpb.FieldDescriptorProto) *descriptorpb.FieldDescriptorProto {
	f.Label = descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()
	return f
}

// dynMsg builds a message with the given fields.
func dynMsg(name string, fields ...*descriptorpb.FieldDescriptorProto) *descriptorpb.DescriptorProto {
	return &descriptorpb.DescriptorProto{Name: proto.String(name), Field: fields}
}

// dynMapOfMsg builds message `name` with one field `m`: map<string, target>.
func dynMapOfMsg(name, target string) *descriptorpb.DescriptorProto {
	entry := &descriptorpb.DescriptorProto{
		Name: proto.String("MEntry"),
		Field: []*descriptorpb.FieldDescriptorProto{
			{
				Name:     proto.String("key"),
				Number:   proto.Int32(1),
				Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
				Type:     descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
				JsonName: proto.String("key"),
			},
			dynMsgField("value", 2, target),
		},
		Options: &descriptorpb.MessageOptions{MapEntry: proto.Bool(true)},
	}
	m := &descriptorpb.FieldDescriptorProto{
		Name:     proto.String("m"),
		Number:   proto.Int32(1),
		Label:    descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum(),
		Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
		TypeName: proto.String(".dyn.v1." + name + ".MEntry"),
		JsonName: proto.String("m"),
	}
	return &descriptorpb.DescriptorProto{
		Name:       proto.String(name),
		Field:      []*descriptorpb.FieldDescriptorProto{m},
		NestedType: []*descriptorpb.DescriptorProto{entry},
	}
}

func assertCyclic(t *testing.T, cycles cycleSet, name protoreflect.FullName, want bool) {
	t.Helper()
	if got := cycles[name]; got != want {
		t.Errorf("cyclic(%s) = %v, want %v", name, got, want)
	}
}

func TestAnalyzeCyclesFixture(t *testing.T) {
	p := loadTestPlugin(t)
	userFile := findFile(t, p, "user.proto")
	cycles := analyzeCycles(userFile.Messages)

	// Top-level AddressDetails references itself through singular, repeated,
	// map, oneof and optional fields. The nested Address.AddressDetails is a
	// different, flat message.
	assertCyclic(t, cycles, "users.v1.AddressDetails", true)
	assertCyclic(t, cycles, "users.v1.Address.AddressDetails", false)
	assertCyclic(t, cycles, "users.v1.Address", false)
	assertCyclic(t, cycles, "users.v1.ComprehensiveUser", false)
	assertCyclic(t, cycles, "users.v1.User", false)

	// Struct/Value/ListValue form a cycle in the descriptor graph, but they
	// map to free-form JSON, so they are never nodes of the analysis.
	assertCyclic(t, cycles, "google.protobuf.Struct", false)
	assertCyclic(t, cycles, "google.protobuf.Value", false)
	assertCyclic(t, cycles, "google.protobuf.ListValue", false)
}

func TestAnalyzeCyclesMutualRecursion(t *testing.T) {
	file := dynFile(t,
		dynMsg("Root", dynMsgField("a", 1, "A")),
		dynMsg("A", dynMsgField("b", 1, "B")),
		dynMsg("B", dynMsgField("a", 1, "A")),
		dynMsg("Leaf"),
	)
	// Only Root is a root: A and B are reached through fields.
	cycles := analyzeCycles([]*protogen.Message{findMessage(t, file, "Root")})

	assertCyclic(t, cycles, "dyn.v1.Root", false)
	assertCyclic(t, cycles, "dyn.v1.A", true)
	assertCyclic(t, cycles, "dyn.v1.B", true)
	assertCyclic(t, cycles, "dyn.v1.Leaf", false)
}

func TestAnalyzeCyclesThroughMapValue(t *testing.T) {
	file := dynFile(t, dynMapOfMsg("Tree", "Tree"))
	cycles := analyzeCycles(file.Messages)
	assertCyclic(t, cycles, "dyn.v1.Tree", true)
}

func TestAnalyzeCyclesDiamondIsAcyclic(t *testing.T) {
	file := dynFile(t,
		dynMsg("A", dynMsgField("b", 1, "B"), dynMsgField("c", 2, "C")),
		dynMsg("B", dynMsgField("d", 1, "D")),
		dynMsg("C", dynMsgField("d", 1, "D")),
		dynMsg("D"),
	)
	cycles := analyzeCycles(file.Messages)
	for _, name := range []protoreflect.FullName{"dyn.v1.A", "dyn.v1.B", "dyn.v1.C", "dyn.v1.D"} {
		assertCyclic(t, cycles, name, false)
	}
}

func TestAnalyzeCyclesIgnoredFieldIsNotAnEdge(t *testing.T) {
	file := dynFile(t, dynMsg("Node", dynIgnored(dynMsgField("self", 1, "Node"))))
	cycles := analyzeCycles(file.Messages)
	assertCyclic(t, cycles, "dyn.v1.Node", false)
}

func TestAnalyzeCyclesVisitsUnreferencedNestedMessages(t *testing.T) {
	// A nested message nobody references by field still generates its own
	// JsonSchema(), so its cyclicity must be decided too.
	outer := dynMsg("Outer")
	outer.NestedType = []*descriptorpb.DescriptorProto{
		dynMsg("Inner", dynMsgField("self", 1, "Outer.Inner")),
	}
	file := dynFile(t, outer)
	cycles := analyzeCycles(file.Messages)
	assertCyclic(t, cycles, "dyn.v1.Outer.Inner", true)
	assertCyclic(t, cycles, "dyn.v1.Outer", false)
}

func TestAnalyzeCyclesGroupFieldIsAnEdge(t *testing.T) {
	// proto2 `optional group Children = 2 { repeated Node node = 1; }`: a
	// group is a message reference, so Node and Node.Children form a cycle.
	node := dynMsg("Node", dynGroupField("children", 2, "Node.Children"))
	node.NestedType = []*descriptorpb.DescriptorProto{
		dynMsg("Children", dynRepeated(dynMsgField("node", 1, "Node"))),
	}
	file := dynFileCustom(t, "dyn/v1/dyn.proto", "proto2", node)
	cycles := analyzeCycles(file.Messages)
	assertCyclic(t, cycles, "dyn.v1.Node", true)
	assertCyclic(t, cycles, "dyn.v1.Node.Children", true)
}

func TestMessageReferences(t *testing.T) {
	// One message with every reference kind in field order — map value,
	// singular, repeated, proto2 group — plus an ignored field and a
	// free-form Struct, which must not count.
	root := dynMapOfMsg("Root", "A") // field 1: map<string, A>
	root.Field = append(root.Field,
		dynMsgField("b", 2, "B"),
		dynRepeated(dynMsgField("as", 3, "A")),
		dynIgnored(dynMsgField("hidden", 4, "C")),
		dynMsgField("payload", 5, ".google.protobuf.Struct"),
		dynGroupField("grp", 6, "Root.Grp"),
	)
	root.NestedType = append(root.NestedType, dynMsg("Grp"))
	file := dynFileCustom(t, "dyn/v1/dyn.proto", "proto2", root, dynMsg("A"), dynMsg("B"), dynMsg("C"))

	var got []string
	for _, m := range messageReferences(findMessage(t, file, "Root")) {
		got = append(got, string(m.Desc.FullName()))
	}
	want := []string{"dyn.v1.A", "dyn.v1.B", "dyn.v1.A", "dyn.v1.Root.Grp"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("messageReferences = %v, want %v", got, want)
	}
}
