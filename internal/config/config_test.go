package config

import (
	"reflect"
	"testing"
)

func TestLoadAPIKeys(t *testing.T) {
	tests := []struct {
		name     string
		apiKeys  string
		apiKey   string
		want     []string
	}{
		{
			name:    "multiple keys",
			apiKeys: "key1,key2,key3",
			want:    []string{"key1", "key2", "key3"},
		},
		{
			name:    "whitespace and empty entries dropped",
			apiKeys: " key1 , ,key2,, ",
			want:    []string{"key1", "key2"},
		},
		{
			name:    "duplicates deduped",
			apiKeys: "key1,key1,key2,key1",
			want:    []string{"key1", "key2"},
		},
		{
			name:    "single key via API_KEYS",
			apiKeys: "only-one",
			want:    []string{"only-one"},
		},
		{
			name:    "fallback to legacy API_KEY",
			apiKey:  "legacy-secret",
			want:    []string{"legacy-secret"},
		},
		{
			name:    "API_KEYS takes precedence over API_KEY",
			apiKeys: "new1,new2",
			apiKey:  "legacy-secret",
			want:    []string{"new1", "new2"},
		},
		{
			name:    "both unset means no auth",
			apiKeys: "",
			want:    nil,
		},
		{
			name:    "blank API_KEYS ignored, fallback applies",
			apiKeys: "  , , ",
			apiKey:  "legacy-secret",
			want:    []string{"legacy-secret"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("API_KEYS", tt.apiKeys)
			t.Setenv("API_KEY", tt.apiKey)
			got := loadAPIKeys()
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("loadAPIKeys() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseKeys(t *testing.T) {
	got := parseKeys(" a ,b,,c , c ")
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseKeys() = %v, want %v", got, want)
	}
}