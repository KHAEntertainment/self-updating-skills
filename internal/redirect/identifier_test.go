package redirect

import "testing"

func TestContainsIdentifier(t *testing.T) {
	cases := []struct {
		text, ident string
		want        bool
	}{
		{"Use api.v2.", "api.v2", true},
		{"api.v2", "api.v2", true},            // whole text
		{"api.v2 is current", "api.v2", true}, // start of text
		{"switch to api.v2", "api.v2", true},  // end of text
		{"Use `api.v2`, not api.v1.", "api.v2", true},
		{"Use api.v2.x instead.", "api.v2", false}, // longer dotted path
		{"Use api.v2-beta.", "api.v2", false},
		{"Use api.v20.", "api.v2", false},
		{"Use api.v10.", "api.v1", false},
		{"Use x.api.v2 here.", "api.v2", false},
		{"Run with --legacy-mode.", "--legacy", false},
		{"Run with --legacy.", "--legacy", true},
		{`Set old\_key.`, "old_key", true}, // Markdown escape
		{"Set old_key\nnow.", "old_key", true},
		{"Order a café.", "café", true}, // NFD text, NFC ident
		{"Order a café.", "café", true}, // NFC text, NFD ident
		{"Order a cafeteria.", "cafe", false},
		{"anything", "", false},
	}
	for _, c := range cases {
		if got := ContainsIdentifier(c.text, c.ident); got != c.want {
			t.Errorf("ContainsIdentifier(%q, %q) = %v, want %v", c.text, c.ident, got, c.want)
		}
	}
}

func TestCanon(t *testing.T) {
	if got := Canon("a\\_b  \n c\\\\d café"); got != "a_b c\\d café" {
		t.Fatalf("Canon: %q", got)
	}
}
