package alipay

import (
	"maps"
	"math"
	"net/url"
	"testing"
)

func TestCentToYuan(t *testing.T) {
	tests := []struct {
		name string
		cent int64
		want string
	}{
		{name: "zero", cent: 0, want: "0.00"},
		{name: "small", cent: 29, want: "0.29"},
		{name: "integer", cent: 3300, want: "33.00"},
		{name: "fraction", cent: 3333, want: "33.33"},
		{name: "negative", cent: -105, want: "-1.05"},
		{name: "max_int64", cent: math.MaxInt64, want: "92233720368547758.07"},
		{name: "min_int64", cent: math.MinInt64, want: "-92233720368547758.08"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := centToYuan(tt.cent); got != tt.want {
				t.Fatalf("centToYuan(%d) = %q, want %q", tt.cent, got, tt.want)
			}
		})
	}
}

func TestYuanToCent(t *testing.T) {
	tests := []struct {
		name string
		yuan string
		want int64
	}{
		{name: "small", yuan: "0.29", want: 29},
		{name: "fraction", yuan: "33.33", want: 3333},
		{name: "whole", yuan: "56.43", want: 5643},
		{name: "with_space", yuan: " 1.20 ", want: 120},
		{name: "missing_whole", yuan: ".99", want: 99},
		{name: "negative", yuan: "-1.05", want: -105},
		{name: "extra_precision_truncated", yuan: "1.234", want: 123},
		{name: "extra_zero_precision", yuan: "0.100", want: 10},
		{name: "max_int64", yuan: "92233720368547758.07", want: math.MaxInt64},
		{name: "overflow", yuan: "92233720368547758.08", want: 0},
		{name: "invalid_text", yuan: "abc", want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := yuanToCent(tt.yuan); got != tt.want {
				t.Fatalf("yuanToCent(%q) = %d, want %d", tt.yuan, got, tt.want)
			}
		})
	}
}

func TestPassbackParamsRoundTrip(t *testing.T) {
	metadata := map[string]string{
		"plain":   "value",
		"amp":     "a&b",
		"equal":   "a=b",
		"space":   "hello world",
		"unicode": "中文",
	}

	encoded := encodePassbackParams(metadata)
	if encoded == "" {
		t.Fatal("encodePassbackParams returned empty string")
	}
	if encoded == url.QueryEscape(encoded) {
		t.Fatalf("encodePassbackParams(%#v) appears double-escaped: %q", metadata, encoded)
	}

	decoded := decodePassbackParams(encoded)
	if !maps.Equal(decoded, metadata) {
		t.Fatalf("decodePassbackParams(%q) = %#v, want %#v", encoded, decoded, metadata)
	}
}

func TestDecodePassbackParamsBackwardCompatible(t *testing.T) {
	raw := "a%3D1%26b%3D2"
	want := map[string]string{"a": "1", "b": "2"}

	if got := decodePassbackParams(raw); !maps.Equal(got, want) {
		t.Fatalf("decodePassbackParams(%q) = %#v, want %#v", raw, got, want)
	}
}
