package agentsdk

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
)

// Validator is an optional interface that config types can implement to
// provide custom validation. If T implements Validate() error, ConfigManager
// will call it during Validate().
type Validator interface {
	Validate() error
}

// fieldMetadata holds cached reflection data for a single struct field.
type fieldMetadata struct {
	jsonName    string
	sensitive   bool
	configurable bool
	fieldIndex  []int
	fieldType   reflect.Type
}

// ConfigManager[T] manages configuration for type T, persisted as JSON.
// It uses struct tags to drive serialization, redaction, and whitelist-based
// field setting:
//
//   - json:"name" — standard JSON serialization and field name mapping
//   - sensitive:"true" — marks field for redaction in Redacted() output
//   - config:"true" — marks field as settable via SetByPath (whitelist)
//
// All reflection metadata is computed once at construction and cached.
type ConfigManager[T any] struct {
	filePath string
	fields   []fieldMetadata
	// jsonName → index into fields (for fast lookup)
	byJSONName map[string]int
}

// NewConfigManager creates a ConfigManager for type T that reads/writes
// the JSON file at filePath. It introspects T's struct tags at creation
// time and caches the field metadata.
func NewConfigManager[T any](filePath string) *ConfigManager[T] {
	var zero T
	t := reflect.TypeOf(zero)

	// If T is a pointer, dereference to get the underlying struct.
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	var fields []fieldMetadata
	byJSONName := make(map[string]int)

	if t.Kind() == reflect.Struct {
		collectFields(t, nil, &fields, byJSONName)
	}

	return &ConfigManager[T]{
		filePath:   filePath,
		fields:     fields,
		byJSONName: byJSONName,
	}
}

// collectFields recursively collects field metadata from a struct type,
// handling nested structs (but not nested struct pointers for simplicity).
func collectFields(
	t reflect.Type,
	parentIndex []int,
	fields *[]fieldMetadata,
	byJSONName map[string]int,
) {
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}

		fullIndex := append(parentIndex, sf.Index...)

		jsonTag := sf.Tag.Get("json")
		jsonName, omitempty := parseJSONTag(jsonTag)
		_ = omitempty // used by json marshal/unmarshal automatically

		// If no json tag or explicitly "-", skip from metadata.
		if jsonName == "-" {
			continue
		}
		if jsonName == "" {
			jsonName = sf.Name
		}

		sensitive := sf.Tag.Get("sensitive") == "true"
		configurable := sf.Tag.Get("config") == "true"

		idx := len(*fields)
		*fields = append(*fields, fieldMetadata{
			jsonName:    jsonName,
			sensitive:   sensitive,
			configurable: configurable,
			fieldIndex:  fullIndex,
			fieldType:   sf.Type,
		})
		byJSONName[jsonName] = idx

		// Recurse into nested structs (not pointers)
		if sf.Type.Kind() == reflect.Struct {
			collectFields(sf.Type, fullIndex, fields, byJSONName)
		}
	}
}

// parseJSONTag splits a json tag like "name,omitempty" into (name, true).
func parseJSONTag(tag string) (name string, omitempty bool) {
	parts := strings.Split(tag, ",")
	name = parts[0]
	for _, p := range parts[1:] {
		if p == "omitempty" {
			omitempty = true
		}
	}
	return
}

// FilePath returns the path to the configuration file.
func (cm *ConfigManager[T]) FilePath() string {
	return cm.filePath
}

// Load reads the JSON file and unmarshals it into a new T value.
// Returns an error wrapping the underlying OS or JSON error with file path context.
func (cm *ConfigManager[T]) Load() (T, error) {
	var zero T

	data, err := os.ReadFile(cm.filePath)
	if err != nil {
		return zero, fmt.Errorf("config: read %q: %w", cm.filePath, err)
	}

	var cfg T
	if err := json.Unmarshal(data, &cfg); err != nil {
		return zero, fmt.Errorf("config: parse %q: %w", cm.filePath, err)
	}

	return cfg, nil
}

// Save marshals cfg to JSON (with indentation) and writes atomically:
// first to a .tmp file, then renames to the final path.
func (cm *ConfigManager[T]) Save(cfg T) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("config: marshal: %w", err)
	}

	dir := filepath.Dir(cm.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("config: create dir %q: %w", dir, err)
	}

	tmpFile := cm.filePath + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return fmt.Errorf("config: write tmp %q: %w", tmpFile, err)
	}

	if err := os.Rename(tmpFile, cm.filePath); err != nil {
		return fmt.Errorf("config: rename %q → %q: %w", tmpFile, cm.filePath, err)
	}

	return nil
}

// Validate checks whether cfg implements the Validator interface and calls
// its Validate() method. Returns nil if T does not implement Validator.
func (cm *ConfigManager[T]) Validate(cfg T) error {
	if v, ok := any(cfg).(Validator); ok {
		return v.Validate()
	}
	return nil
}

// Redacted returns a deep copy of cfg with all sensitive:"true" fields
// replaced by "***". Non-sensitive fields are preserved as-is.
func (cm *ConfigManager[T]) Redacted(cfg T) T {
	// Deep copy via JSON round-trip
	data, err := json.Marshal(cfg)
	if err != nil {
		return cfg
	}
	var copy T
	if err := json.Unmarshal(data, &copy); err != nil {
		return cfg
	}

	v := reflect.ValueOf(&copy)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	for _, fm := range cm.fields {
		if fm.sensitive {
			f := v.FieldByIndex(fm.fieldIndex)
			if f.CanSet() && f.Kind() == reflect.String {
				f.SetString("***")
			}
		}
	}

	return copy
}

// SetByPath sets a field identified by its json tag name to the given string
// value, converting to the field's type. Supported types: string, int, float64, bool.
// Returns an error if the jsonPath is not in the config:"true" whitelist or if
// the value cannot be converted to the target type.
func (cm *ConfigManager[T]) SetByPath(cfg *T, jsonPath, value string) error {
	idx, ok := cm.byJSONName[jsonPath]
	if !ok {
		return fmt.Errorf("config: unknown field %q", jsonPath)
	}

	fm := cm.fields[idx]
	if !fm.configurable {
		return fmt.Errorf("config: field %q is not configurable (not in whitelist)", jsonPath)
	}

	v := reflect.ValueOf(cfg)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	f := v.FieldByIndex(fm.fieldIndex)
	if !f.CanSet() {
		return fmt.Errorf("config: field %q cannot be set (unexported)", jsonPath)
	}

	converted, err := convertStringValue(value, fm.fieldType.Kind())
	if err != nil {
		return fmt.Errorf("config: convert %q for field %q: %w", value, jsonPath, err)
	}

	f.Set(reflect.ValueOf(converted))
	return nil
}

// Whitelist returns the json tag names of all fields marked config:"true".
// These are the fields that can be set via SetByPath.
func (cm *ConfigManager[T]) Whitelist() []string {
	var names []string
	for _, fm := range cm.fields {
		if fm.configurable {
			names = append(names, fm.jsonName)
		}
	}
	return names
}

// convertStringValue converts a string value to the appropriate Go type
// based on the target reflect.Kind.
func convertStringValue(value string, kind reflect.Kind) (interface{}, error) {
	switch kind {
	case reflect.String:
		return value, nil
	case reflect.Int:
		v, err := strconv.Atoi(value)
		if err != nil {
			return nil, fmt.Errorf("invalid int: %q", value)
		}
		return v, nil
	case reflect.Float64:
		v, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid float64: %q", value)
		}
		return v, nil
	case reflect.Bool:
		v, err := strconv.ParseBool(value)
		if err != nil {
			return nil, fmt.Errorf("invalid bool: %q", value)
		}
		return v, nil
	default:
		return nil, fmt.Errorf("unsupported type %s", kind)
	}
}

// ListRedacted loads the config, redacts sensitive fields, and returns it
// as a generic interface{} suitable for JSONL emission.
// This makes *ConfigManager[T] satisfy the ConfigProvider interface.
func (cm *ConfigManager[T]) ListRedacted() (interface{}, error) {
	cfg, err := cm.Load()
	if err != nil {
		return nil, err
	}
	redacted := cm.Redacted(cfg)
	return redacted, nil
}

// Set loads the config, validates the jsonPath against the whitelist,
// sets the value, validates the result, and saves.
// This makes *ConfigManager[T] satisfy the ConfigProvider interface.
func (cm *ConfigManager[T]) Set(jsonPath, value string) error {
	cfg, err := cm.Load()
	if err != nil {
		return err
	}

	if err := cm.SetByPath(&cfg, jsonPath, value); err != nil {
		return err
	}

	if err := cm.Validate(cfg); err != nil {
		return err
	}

	return cm.Save(cfg)
}
