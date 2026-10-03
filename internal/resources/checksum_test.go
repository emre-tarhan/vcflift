package resources

import (
	"strings"
	"testing"
)

func TestParseChecksumIndex(t *testing.T) {
	in := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  one.txt\n9573fe0b0a4715f0dd4fa62d5bd7810d  GRCh38_to_GRCh37.chain.gz\n"
	got, err := parseChecksumIndex(strings.NewReader(in), "GRCh38_to_GRCh37.chain.gz")
	if err != nil {
		t.Fatal(err)
	}
	if got != "9573fe0b0a4715f0dd4fa62d5bd7810d" {
		t.Fatalf("got %q", got)
	}
}
