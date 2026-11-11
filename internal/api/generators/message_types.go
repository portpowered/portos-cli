package main

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"go/format"
	"log"
	"os"
	"sort"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"
)

// MessageDefinition represents a message definition from YAML
type MessageDefinition struct {
	Namespace      string `yaml:"x-namespace"`
	Name           string `yaml:"x-name"`
	Version        string `yaml:"x-version"`
	Type           string `yaml:"x-type"`
	Directionality string `yaml:"x-directionality"`
	Title          string `yaml:"title"`
	Description    string `yaml:"description"`
}

// MessageTypeInfo contains information about a message type for code generation
type MessageTypeInfo struct {
	ConstName       string
	Namespace       string
	Name            string
	Version         string
	Type            string
	Directionality  string
	Title           string
	Description     string
	FuncName        string
	PayloadTypeName string
	NamespaceConst  string
}

// AttributeTypeInfo contains information about an attribute type for code generation
type AttributeTypeInfo struct {
	ConstName       string
	Namespace       string
	Name            string
	Version         string
	Type            string
	Title           string
	Description     string
	FuncName        string
	PayloadTypeName string
	NamespaceConst  string
	AttributeName   string // The attribute name in context of the interface
}

// NamespaceInfo contains information about a namespace for code generation
type NamespaceInfo struct {
	ConstName  string
	Namespace  string
	Messages   []MessageTypeInfo
	Attributes []AttributeTypeInfo
}

//go:embed message_types.tmpl
var generatedFileTemplate string

type templateData struct {
	Namespaces    []NamespaceInfo
	AllMessages   []MessageTypeInfo
	AllAttributes []AttributeTypeInfo
}

func main() {
	if len(os.Args) != 2 {
		log.Fatalf("usage: message_types <openapi.yaml>")
	}
	doc, err := loadSpec(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	messageDefs, attributeDefs, err := readDefinitions(doc)
	if err != nil {
		log.Fatalf("Failed to read definitions: %v", err)
	}
	data := groupDefinitions(messageDefs, attributeDefs)
	formatted, err := renderDefinitions(data)
	if err != nil {
		log.Fatal(err)
	}
	outputFile := "message_types.gen.go"
	if err := writeGeneratedFile(outputFile, formatted); err != nil {
		log.Fatalf("Failed to write generated file: %v", err)
	}
	fmt.Printf("Generated %s with %d namespaces, %d message types, and %d attribute types\n", outputFile, len(data.Namespaces), len(data.AllMessages), len(data.AllAttributes))
}

// groupDefinitions orders namespaces and their members before rendering.
func groupDefinitions(messageDefs []MessageTypeInfo, attributeDefs []AttributeTypeInfo) templateData {
	// Group by namespace
	namespaceMap := make(map[string]NamespaceInfo)

	// Process messages
	for _, msg := range messageDefs {
		ns, exists := namespaceMap[msg.Namespace]
		if !exists {
			ns = NamespaceInfo{
				ConstName:  generateConstName(msg.Namespace),
				Namespace:  msg.Namespace,
				Messages:   []MessageTypeInfo{},
				Attributes: []AttributeTypeInfo{},
			}
		}
		ns.Messages = append(ns.Messages, msg)
		namespaceMap[msg.Namespace] = ns
	}

	// Process attributes
	for _, attribute := range attributeDefs {
		ns, exists := namespaceMap[attribute.Namespace]
		if !exists {
			ns = NamespaceInfo{
				ConstName:  generateConstName(attribute.Namespace),
				Namespace:  attribute.Namespace,
				Messages:   []MessageTypeInfo{},
				Attributes: []AttributeTypeInfo{},
			}
		}
		ns.Attributes = append(ns.Attributes, attribute)
		namespaceMap[attribute.Namespace] = ns
	}

	// Convert to sorted slice for consistent output
	var namespaces []NamespaceInfo
	for _, ns := range namespaceMap {
		// Sort messages by name for consistent output
		sort.Slice(ns.Messages, func(i, j int) bool {
			return ns.Messages[i].Name < ns.Messages[j].Name
		})

		// Sort attributes by name for consistent output
		sort.Slice(ns.Attributes, func(i, j int) bool {
			return ns.Attributes[i].Name < ns.Attributes[j].Name
		})

		namespaces = append(namespaces, ns)
	}

	// Sort namespaces by namespace string for consistent output
	sort.Slice(namespaces, func(i, j int) bool {
		return namespaces[i].Namespace < namespaces[j].Namespace
	})

	// Collect all messages for the MessageTypeInfo map
	var allMessages []MessageTypeInfo
	var allAttributes []AttributeTypeInfo
	for _, ns := range namespaces {
		allMessages = append(allMessages, ns.Messages...)
		allAttributes = append(allAttributes, ns.Attributes...)
	}

	return templateData{Namespaces: namespaces, AllMessages: allMessages, AllAttributes: allAttributes}
}

// renderDefinitions preserves template metadata while producing formatted Go source.
func renderDefinitions(data templateData) ([]byte, error) {
	tmpl, err := template.New("generated").Parse(generatedFileTemplate)
	if err != nil {
		return nil, fmt.Errorf("failed to parse template: %w", err)
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("failed to execute template: %w", err)
	}
	formatted, err := format.Source([]byte(buf.String()))
	if err != nil {
		return nil, fmt.Errorf("failed to format generated code: %w", err)
	}
	return formatted, nil
}

func writeGeneratedFile(path string, content []byte) error {
	existing, err := os.ReadFile(path)
	if err == nil && bytes.Equal(existing, content) {
		return nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.WriteFile(path, content, 0644)
}

// loadSpec parses an OpenAPI document into a generic map.
func loadSpec(path string) (map[string]any, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", path, err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", path, err)
	}
	return doc, nil
}

func stringField(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

// readDefinitions collects the message and attribute definitions carried by
// components.schemas entries whose x-type is "message" or "attribute-definition".
func readDefinitions(doc map[string]any) ([]MessageTypeInfo, []AttributeTypeInfo, error) {
	components, _ := doc["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)
	names := make([]string, 0, len(schemas))
	for name := range schemas {
		names = append(names, name)
	}
	sort.Strings(names)

	var messageDefs []MessageTypeInfo
	var attributeDefs []AttributeTypeInfo
	for _, schemaName := range names {
		schema, ok := schemas[schemaName].(map[string]any)
		if !ok {
			continue
		}
		def := MessageDefinition{
			Namespace:      stringField(schema, "x-namespace"),
			Name:           stringField(schema, "x-name"),
			Version:        stringField(schema, "x-version"),
			Type:           stringField(schema, "x-type"),
			Directionality: stringField(schema, "x-directionality"),
			Title:          stringField(schema, "title"),
			Description:    stringField(schema, "description"),
		}
		switch def.Type {
		case "message":
			payloadTypeName := generatePayloadTypeName(def.Namespace, def.Name)
			messageDefs = append(messageDefs, MessageTypeInfo{
				ConstName:       generateMessageConstName(def.Namespace, def.Name),
				Namespace:       def.Namespace,
				Name:            def.Name,
				Version:         def.Version,
				Type:            def.Type,
				Directionality:  def.Directionality,
				Title:           def.Title,
				Description:     def.Description,
				FuncName:        strings.TrimSuffix(payloadTypeName, "Message"),
				PayloadTypeName: payloadTypeName,
				NamespaceConst:  generateConstName(def.Namespace),
			})
		case "attribute-definition":
			// The OpenAPI schema name is the generated Go type name.
			attributeDefs = append(attributeDefs, AttributeTypeInfo{
				ConstName:       generateAttributeConstName(def.Namespace, def.Name),
				Namespace:       def.Namespace,
				Name:            def.Name,
				Version:         def.Version,
				Type:            def.Type,
				Title:           def.Title,
				Description:     def.Description,
				FuncName:        strings.TrimSuffix(schemaName, "Attribute"),
				PayloadTypeName: schemaName,
				NamespaceConst:  generateConstName(def.Namespace),
				AttributeName:   def.Name,
			})
		}
	}
	return messageDefs, attributeDefs, nil
}

func generateConstName(namespace string) string {
	// Convert namespace to constant name
	// e.g., "port1/systems/zero/capability-interfaces/power" -> "INTERFACE_PORTOS_POWER"
	parts := strings.Split(namespace, "/")
	if len(parts) >= 4 && parts[3] == "capability-interfaces" {
		interfaceName := strings.ToUpper(strings.ReplaceAll(parts[4], "-", "_"))
		return "INTERFACE_PORTOS_" + interfaceName
	}

	// Fallback for other namespace formats
	clean := strings.ReplaceAll(namespace, "/", "_")
	clean = strings.ReplaceAll(clean, "-", "_")
	clean = strings.ToUpper(clean)
	return "INTERFACE_" + clean
}

func generateMessageConstName(namespace, messageName string) string {
	// Generate constant name for message
	// e.g., "port1/systems/zero/capability-interfaces/power" + "turn-on" -> "INTERFACE_PORTOS_POWER_TURN_ON"
	namespaceConst := generateConstName(namespace)
	messageConst := strings.ToUpper(strings.ReplaceAll(messageName, "-", "_"))
	return namespaceConst + "_" + messageConst
}

func generateFuncName(messageName string) string {
	// Convert kebab-case to CamelCase
	// e.g., "turn-on" -> "TurnOn", "enumerate-files-response" -> "EnumerateFilesResponse"
	parts := strings.Split(messageName, "-")
	for i, part := range parts {
		if len(part) > 0 {
			parts[i] = strings.ToUpper(part[:1]) + part[1:]
		}
	}
	return strings.Join(parts, "")
}

func generatePayloadTypeName(namespace, messageName string) string {
	// Generate payload type name
	// e.g., "port1/systems/zero/capability-interfaces/power" + "turn-on"
	// -> "PowerTurnOnMessagePayload"

	// Extract interface name from namespace
	namespaceParts := strings.Split(namespace, "/")
	var interfaceName string
	if len(namespaceParts) >= 4 && namespaceParts[3] == "capability-interfaces" {
		// Convert kebab-case to CamelCase properly
		interfaceName = generateFuncName(namespaceParts[4])

	}

	// Convert message name to CamelCase
	funcName := generateFuncName(messageName)

	return interfaceName + "Interface" + funcName + "Message"
}

func generateAttributeConstName(namespace, attributeName string) string {
	// Generate constant name for attribute
	// e.g., "port1/systems/zero/capability-interfaces/power" + "power" -> "INTERFACE_PORTOS_POWER_ATTRIBUTE_POWER"
	namespaceConst := generateConstName(namespace)
	attributeConst := strings.ToUpper(strings.ReplaceAll(attributeName, "-", "_"))
	return namespaceConst + "_ATTRIBUTE_" + attributeConst
}
