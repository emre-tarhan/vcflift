package gvcf

import "testing"

func TestBlockValidation(t *testing.T) {
	if err := (Block{Contig: "chr1", Start: 10, End: 20}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (Block{Contig: "chr1", Start: 20, End: 10}).Validate(); err == nil {
		t.Fatal("expected invalid block")
	}
}

func TestDefaultStrategyIsStrict(t *testing.T) {
	s := DefaultStrategy()
	if !s.LiftEndCoordinates || !s.RequireContiguousMapping || !s.RejectAmbiguousBlocks {
		t.Fatalf("unexpected strategy: %+v", s)
	}
}
