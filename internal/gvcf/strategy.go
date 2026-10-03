package gvcf

import "fmt"

// Strategy describes the invariants VCF Lift must uphold for gVCF conversion.
// It separates gVCF reference blocks from ordinary variant records.
type Strategy struct {
	LiftEndCoordinates       bool
	RequireContiguousMapping bool
	SplitAtChainBoundaries   bool
	RejectAmbiguousBlocks    bool
	PreserveNonRefAllele     bool
}

func DefaultStrategy() Strategy {
	return Strategy{
		LiftEndCoordinates:       true,
		RequireContiguousMapping: true,
		SplitAtChainBoundaries:   true,
		RejectAmbiguousBlocks:    true,
		PreserveNonRefAllele:     true,
	}
}

type Block struct {
	Contig string
	Start  int64 // 1-based inclusive
	End    int64 // 1-based inclusive
}

func (b Block) Validate() error {
	if b.Contig == "" {
		return fmt.Errorf("gVCF block has empty contig")
	}
	if b.Start < 1 || b.End < b.Start {
		return fmt.Errorf("invalid gVCF block %s:%d-%d", b.Contig, b.Start, b.End)
	}
	return nil
}

// Exact gVCF support requires block-aware mapping. Mapping only POS and END is
// insufficient when an interval crosses a chain gap or changes mapping context.
func NeedsBlockAwareLiftover(b Block) bool {
	return b.End > b.Start
}
