package main

import (
	"bytes"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestMessageTypeGeneration(t *testing.T) {
	doc, err := loadSpec("../../../../packages/reference-data/generated/public/docs/references/openapi.yaml")
	if err != nil {
		t.Fatalf("Failed to load the public spec: %v", err)
	}
	messageDefs, _, err := readDefinitions(doc)
	if err != nil {
		t.Fatalf("Failed to read message definitions: %v", err)
	}
	if len(messageDefs) == 0 {
		t.Fatal("Expected to find message definitions, but found none")
	}
	for _, msg := range messageDefs {
		if generateConstName(msg.Namespace) == "" || generateMessageConstName(msg.Namespace, msg.Name) == "" {
			t.Errorf("empty constant name for %s/%s", msg.Namespace, msg.Name)
		}
	}
}

func TestGenerateConstName(t *testing.T) {
	tests := []struct {
		namespace string
		expected  string
	}{
		{
			namespace: "port1/systems/zero/capability-interfaces/power",
			expected:  "INTERFACE_PORTOS_POWER",
		},
		{
			namespace: "port1/systems/zero/capability-interfaces/media-controls",
			expected:  "INTERFACE_PORTOS_MEDIA_CONTROLS",
		},
		{
			namespace: "port1/systems/zero/capability-interfaces/media-player",
			expected:  "INTERFACE_PORTOS_MEDIA_PLAYER",
		},
	}

	for _, test := range tests {
		result := generateConstName(test.namespace)
		if result != test.expected {
			t.Errorf("generateConstName(%s) = %s, expected %s", test.namespace, result, test.expected)
		}
	}
}

func TestGenerateMessageConstName(t *testing.T) {
	tests := []struct {
		namespace   string
		messageName string
		expected    string
	}{
		{
			namespace:   "port1/systems/zero/capability-interfaces/power",
			messageName: "turn-on",
			expected:    "INTERFACE_PORTOS_POWER_TURN_ON",
		},
		{
			namespace:   "port1/systems/zero/capability-interfaces/media-controls",
			messageName: "play",
			expected:    "INTERFACE_PORTOS_MEDIA_CONTROLS_PLAY",
		},
		{
			namespace:   "port1/systems/zero/capability-interfaces/media-controls",
			messageName: "next-track",
			expected:    "INTERFACE_PORTOS_MEDIA_CONTROLS_NEXT_TRACK",
		},
	}

	for _, test := range tests {
		result := generateMessageConstName(test.namespace, test.messageName)
		if result != test.expected {
			t.Errorf("generateMessageConstName(%s, %s) = %s, expected %s", test.namespace, test.messageName, result, test.expected)
		}
	}
}

func TestWriteGeneratedFileLeavesUnchangedContentUntouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), "message_types.gen.go")
	content := []byte("generated content")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2023, time.January, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}

	if err := writeGeneratedFile(path, content); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, content) {
		t.Fatalf("generated content changed: got %q, want %q", actual, content)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(stamp) {
		t.Fatalf("unchanged generated file was rewritten: got mtime %s, want %s", info.ModTime(), stamp)
	}
}

func TestWriteGeneratedFileReplacesChangedContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "message_types.gen.go")
	if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := writeGeneratedFile(path, []byte("new")); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(actual) != "new" {
		t.Fatalf("generated content was not replaced: got %q, want %q", actual, "new")
	}
}

func TestTemplateEscapesMetadataAndPreservesItsText(t *testing.T) {
	namespace := "port1/systems/zero/capability-interfaces/power"
	namespaceConst := generateConstName(namespace)
	message := MessageTypeInfo{Namespace: namespace, NamespaceConst: namespaceConst, Name: "turn-on", Version: "1.0", Type: "message", Directionality: "inbound",
		ConstName: generateMessageConstName(namespace, "turn-on"), FuncName: "PowerInterfaceTurnOn", PayloadTypeName: "PowerInterfaceTurnOnMessage",
		Title: "Turn \"on\" \\ desktop\nmode", Description: "First line\nSecond \"quoted\" C:\\fixtures\\payload\r\nlast\tcolumn"}
	attribute := AttributeTypeInfo{Namespace: namespace, NamespaceConst: namespaceConst, Name: "power", AttributeName: "power", Version: "1.0", Type: "attribute-definition",
		ConstName: generateAttributeConstName(namespace, "power"), FuncName: "PowerInterfacePower", PayloadTypeName: "PowerInterfacePowerAttribute",
		Title: "Power \"state\" \\ shelf\nvalue", Description: "Café\nState \\ path \"value\"\r\nlast\tcolumn"}
	formatted, err := renderDefinitions(groupDefinitions([]MessageTypeInfo{message}, []AttributeTypeInfo{attribute}))
	if err != nil {
		t.Fatalf("valid metadata did not generate Go: %v", err)
	}
	files := token.NewFileSet()
	output, err := parser.ParseFile(files, "message_types.gen.go", formatted, 0)
	if err != nil {
		t.Fatal(err)
	}
	assertMetadataStrings(t, output, "MessagesTypes", map[string]string{"Title": message.Title, "Description": message.Description})
	assertMetadataStrings(t, output, "AttributesTypes", map[string]string{"Title": attribute.Title, "Description": attribute.Description})
	stub, err := parser.ParseFile(files, "models.go", `package api
type Message struct { Type *string; Header *MessageHeader; Body map[string]interface{} }
type MessageHeader struct { Namespace, Name, Version string }
type PowerInterfaceTurnOnMessage struct{}
type PowerInterfacePowerAttribute struct{}
`, 0)
	if err != nil {
		t.Fatal(err)
	}
	checker := types.Config{Importer: importer.Default()}
	if _, err := checker.Check("fixture", files, []*ast.File{output, stub}, nil); err != nil {
		t.Fatalf("generated constructors and metadata do not compile: %v", err)
	}
}

func assertMetadataStrings(t *testing.T, file *ast.File, name string, expected map[string]string) {
	t.Helper()
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		value, ok := node.(*ast.ValueSpec)
		if !ok || len(value.Names) != 1 || value.Names[0].Name != name {
			return true
		}
		found = true
		entry := value.Values[0].(*ast.CompositeLit).Elts[0].(*ast.KeyValueExpr).Value.(*ast.CompositeLit)
		for _, field := range entry.Elts {
			pair := field.(*ast.KeyValueExpr)
			key := pair.Key.(*ast.Ident).Name
			text, err := strconv.Unquote(pair.Value.(*ast.BasicLit).Value)
			if err != nil {
				t.Fatal(err)
			}
			if want, check := expected[key]; check && text != want {
				t.Errorf("%s.%s changed text: got %q, want %q", name, key, text, want)
			}
		}
		return false
	})
	if !found {
		t.Fatalf("generated %s metadata missing", name)
	}
}

func TestGroupDefinitionsOrdersMixedAndAttributeOnlyNamespaces(t *testing.T) {
	messages := []MessageTypeInfo{{Namespace: "z", Name: "later"}, {Namespace: "a", Name: "second"}, {Namespace: "a", Name: "first"}}
	attributes := []AttributeTypeInfo{{Namespace: "b", Name: "only"}, {Namespace: "a", Name: "z"}, {Namespace: "a", Name: "a"}}
	data := groupDefinitions(messages, attributes)
	if len(data.Namespaces) != 3 || data.Namespaces[0].Namespace != "a" || data.Namespaces[1].Namespace != "b" || data.Namespaces[2].Namespace != "z" {
		t.Fatalf("namespace ordering or attribute-only namespace lost: %+v", data.Namespaces)
	}
	if data.AllMessages[0].Name != "first" || data.AllMessages[1].Name != "second" || data.AllMessages[2].Name != "later" {
		t.Fatalf("message metadata order changed: %+v", data.AllMessages)
	}
	if data.AllAttributes[0].Name != "a" || data.AllAttributes[1].Name != "z" || data.AllAttributes[2].Name != "only" {
		t.Fatalf("attribute metadata order changed: %+v", data.AllAttributes)
	}
	if messages[0].Namespace != "z" || attributes[0].Namespace != "b" {
		t.Fatal("grouping changed caller definition order")
	}
}

func TestReadDefinitionsFromOpenAPI(t *testing.T) {
	doc := map[string]any{
		"components": map[string]any{
			"schemas": map[string]any{
				"RemoteControlInterfaceMoveMessage": map[string]any{
					"x-namespace":      "port1/systems/zero/capability-interfaces/remote-control",
					"x-name":           "move",
					"x-version":        "1.0",
					"x-type":           "message",
					"x-directionality": "in",
					"title":            "Move",
				},
				"RemoteControlInterfaceSessionAttribute": map[string]any{
					"x-namespace": "port1/systems/zero/capability-interfaces/remote-control",
					"x-name":      "session",
					"x-version":   "1.0",
					"x-type":      "attribute-definition",
				},
				"Unrelated": map[string]any{"type": "object"},
			},
		},
	}
	messages, attributes, err := readDefinitions(doc)
	if err != nil {
		t.Fatalf("readDefinitions: %v", err)
	}
	if len(messages) != 1 || len(attributes) != 1 {
		t.Fatalf("got %d messages, %d attributes", len(messages), len(attributes))
	}
	m := messages[0]
	if m.Namespace != "port1/systems/zero/capability-interfaces/remote-control" || m.Name != "move" ||
		m.Directionality != "in" || m.PayloadTypeName != "RemoteControlInterfaceMoveMessage" {
		t.Errorf("unexpected message: %+v", m)
	}
	if attributes[0].Name != "session" || attributes[0].PayloadTypeName != "RemoteControlInterfaceSessionAttribute" {
		t.Errorf("unexpected attribute: %+v", attributes[0])
	}
}
