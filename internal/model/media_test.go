package model

import "testing"

func TestNormalizeMediaCode(t *testing.T) {
	tests := []struct {
		name   string
		source string
		input  string
		want   string
	}{
		{name: "javdb uppercase", source: "javdb", input: "abp-123", want: "ABP-123"},
		{name: "javdb underscore", source: "javdb", input: "t28_123", want: "T28_123"},
		{name: "javdb compact alphanumeric", source: "javdb", input: "hamesamurai0258", want: "HAMESAMURAI0258"},
		{name: "fc2 bare", source: "fc2", input: "1234567", want: "FC2-PPV-1234567"},
		{name: "fc2 full", source: "fc2", input: "fc2-ppv-1234567", want: "FC2-PPV-1234567"},
		{name: "pornhub preserves key", source: "pornhub", input: "ph5fAbC", want: "ph5fAbC"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeMediaCode(tt.source, tt.input)
			if err != nil {
				t.Fatalf("NormalizeMediaCode() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("NormalizeMediaCode() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNormalizeMediaCodeRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		source string
		input  string
	}{
		{source: "javdb", input: "ABP 123"},
		{source: "javdb", input: "../ABP-123"},
		{source: "fc2", input: "FC2-PPV-ABC"},
		{source: "pornhub", input: "../view-key"},
		{source: "unknown", input: "ABP-123"},
	}

	for _, tt := range tests {
		if _, err := NormalizeMediaCode(tt.source, tt.input); err == nil {
			t.Fatalf("NormalizeMediaCode(%q, %q) accepted invalid value", tt.source, tt.input)
		}
	}
}

func TestBuildMediaFileName(t *testing.T) {
	tests := []struct {
		name      string
		code      string
		partIndex int
		partCount int
		want      string
	}{
		{name: "single", code: "ABP-123", partIndex: 1, partCount: 1, want: "ABP-123.mp4"},
		{name: "multipart", code: "ABP-123", partIndex: 2, partCount: 3, want: "ABP-123-cd2.mp4"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := BuildMediaFileName(tt.code, tt.partIndex, tt.partCount)
			if err != nil {
				t.Fatalf("BuildMediaFileName() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("BuildMediaFileName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildMediaFileNameRejectsInvalidIdentity(t *testing.T) {
	tests := []struct {
		code      string
		partIndex int
		partCount int
	}{
		{code: "../ABP-123", partIndex: 1, partCount: 1},
		{code: "ABP/123", partIndex: 1, partCount: 1},
		{code: "", partIndex: 1, partCount: 1},
		{code: "ABP-123", partIndex: 0, partCount: 2},
		{code: "ABP-123", partIndex: 3, partCount: 2},
		{code: "ABP-123", partIndex: 1, partCount: 0},
	}

	for _, tt := range tests {
		if _, err := BuildMediaFileName(tt.code, tt.partIndex, tt.partCount); err == nil {
			t.Fatalf("BuildMediaFileName(%q, %d, %d) accepted invalid identity", tt.code, tt.partIndex, tt.partCount)
		}
	}
}

func TestBuildMediaTitle(t *testing.T) {
	tests := []struct {
		name       string
		code       string
		raw        string
		translated string
		want       string
	}{
		{name: "translated", code: "ABP-123", raw: "原题", translated: "译题", want: "ABP-123 译题"},
		{name: "raw fallback", code: "ABP-123", raw: "原题", want: "ABP-123 原题"},
		{name: "code fallback", code: "ABP-123", want: "ABP-123"},
		{name: "trim title", code: "ABP-123", translated: "  译题  ", want: "ABP-123 译题"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BuildMediaTitle(tt.code, tt.raw, tt.translated); got != tt.want {
				t.Fatalf("BuildMediaTitle() = %q, want %q", got, tt.want)
			}
		})
	}
}
