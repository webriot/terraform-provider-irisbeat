// SPDX-License-Identifier: AGPL-3.0-only
package provider

import "testing"

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
