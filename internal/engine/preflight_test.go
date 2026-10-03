package engine

import "testing"

func TestParseBCFToolsVersion(t *testing.T) {
	cases := map[string]string{
		"bcftools 1.24":                    "1.24",
		"bcftools 1.24\nUsing htslib 1.24": "1.24",
		"bcftools v1.24":                   "1.24",
	}
	for in, want := range cases {
		if got := ParseBCFToolsVersion(in); got != want {
			t.Fatalf("ParseBCFToolsVersion(%q)=%q want %q", in, got, want)
		}
	}
}

func TestRequireVersionRejectsOtherVersion(t *testing.T) {
	if err := RequireVersion(Installation{Version: "bcftools 1.23"}, RequiredBCFToolsVersion); err == nil {
		t.Fatal("expected version rejection")
	}
	if err := RequireVersion(Installation{Version: "bcftools 1.24"}, RequiredBCFToolsVersion); err != nil {
		t.Fatal(err)
	}
}
