package provider

import (
	"encoding/json"
	"fmt"

	sigsyaml "sigs.k8s.io/yaml"
)

// marshalYAML marshals v to YAML using sigs.k8s.io/yaml (which round-trips via JSON).
func marshalYAML(v interface{}) ([]byte, error) {
	data, err := sigsyaml.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshaling YAML: %w", err)
	}
	return data, nil
}

// unmarshalYAML unmarshals YAML data into v.
func unmarshalYAML(data []byte, v interface{}) error {
	if err := sigsyaml.Unmarshal(data, v); err != nil {
		return fmt.Errorf("unmarshaling YAML: %w", err)
	}
	return nil
}

// yamlToUnstructured converts a YAML string to a map[string]interface{}.
func yamlToUnstructured(yamlStr string) (map[string]interface{}, error) {
	var result map[string]interface{}
	if err := sigsyaml.Unmarshal([]byte(yamlStr), &result); err != nil {
		return nil, fmt.Errorf("converting YAML to unstructured: %w", err)
	}
	return result, nil
}

// unstructuredToYAML converts a map[string]interface{} to a YAML string.
func unstructuredToYAML(obj map[string]interface{}) (string, error) {
	data, err := sigsyaml.Marshal(obj)
	if err != nil {
		return "", fmt.Errorf("converting unstructured to YAML: %w", err)
	}
	return string(data), nil
}

// jsonStringToMap parses a JSON string into a map.
func jsonStringToMap(s string) (map[string]interface{}, error) {
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(s), &result); err != nil {
		return nil, fmt.Errorf("parsing JSON: %w", err)
	}
	return result, nil
}

// mapToJSONString converts a map to a JSON string.
func mapToJSONString(m map[string]interface{}) (string, error) {
	data, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("serializing to JSON: %w", err)
	}
	return string(data), nil
}
