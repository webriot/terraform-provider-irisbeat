// SPDX-License-Identifier: AGPL-3.0-only
package provider

import (
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestNormalizeHostname(t *testing.T) {
	for input, want := range map[string]string{
		"İ.example.com":           "i\u0307.example.com",
		"K.EXAMPLE.COM":           "k.example.com",
		"[2001:0DB8:0:0:0:0:0:1]": "2001:db8::1",
	} {
		if got := normalizeHostname(input); got != want {
			t.Errorf("normalizeHostname(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestValidateHTTPTarget(t *testing.T) {
	const prefix = "https://example.com/"
	for _, tc := range []struct {
		name string
		url  types.String
		want string
	}{
		{"unknown", types.StringUnknown(), ""},
		{"missing", types.StringNull(), "Missing HTTP target"},
		{"empty", types.StringValue(""), "Invalid HTTP target"},
		{"relative", types.StringValue("not-a-url"), "Invalid HTTP target"},
		{"scheme", types.StringValue("ftp://example.com"), "Invalid HTTP target"},
		{"username", types.StringValue("https://user@example.com"), "Invalid HTTP target"},
		{"password", types.StringValue("https://:password@example.com"), "Invalid HTTP target"},
		{"normalizable", types.StringValue(" HTTPS:EXAMPLE.com:443#fragment "), ""},
		{"max length", types.StringValue(prefix + strings.Repeat("a", 2048-len(prefix))), ""},
		{"non-ASCII", types.StringValue(prefix + strings.Repeat("é", 2048-len(prefix))), ""},
		{"URI too long", types.StringValue(prefix + strings.Repeat("a", 2049-len(prefix))), "Invalid HTTP target"},
		{"UTF-16 pair exceeds limit", types.StringValue(prefix + strings.Repeat("a", 2047-len(prefix)) + "😀"), "Invalid HTTP target"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var diags diag.Diagnostics
			validateHTTPTarget(tc.url, &diags)
			if tc.want == "" {
				if len(diags) != 0 {
					t.Fatal("expected the target to pass validation")
				}
				return
			}
			if len(diags) != 1 || diags[0].Summary() != tc.want {
				t.Fatalf("expected the %q diagnostic", tc.want)
			}
			if tc.url.ValueString() != "" && strings.Contains(diags[0].Detail(), tc.url.ValueString()) {
				t.Fatal("diagnostic included the configured URL")
			}
		})
	}
}
