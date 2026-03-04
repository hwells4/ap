package resolve

import (
	"testing"

	"gopkg.in/yaml.v3"
)

type yamlFixture struct {
	Name   string   `yaml:"name"`
	Values []string `yaml:"values"`
}

func TestYAMLv3MarshalUnmarshalRoundTrip(t *testing.T) {
	original := yamlFixture{
		Name:   "ap",
		Values: []string{"one", "two"},
	}

	encoded, err := yaml.Marshal(original)
	if err != nil {
		t.Fatalf("yaml.Marshal() error = %v", err)
	}

	var decoded yamlFixture
	if err := yaml.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("yaml.Unmarshal() error = %v", err)
	}

	if decoded.Name != original.Name {
		t.Fatalf("decoded.Name = %q, want %q", decoded.Name, original.Name)
	}
	if len(decoded.Values) != len(original.Values) {
		t.Fatalf("decoded.Values length = %d, want %d", len(decoded.Values), len(original.Values))
	}
	for i := range original.Values {
		if decoded.Values[i] != original.Values[i] {
			t.Fatalf("decoded.Values[%d] = %q, want %q", i, decoded.Values[i], original.Values[i])
		}
	}
}
