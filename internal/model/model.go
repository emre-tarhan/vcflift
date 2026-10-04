package model

import (
	"time"

	"github.com/emre-tarhan/vcflift/internal/ledger"
)

type FileKind string

const (
	FileKindUnknown FileKind = "unknown"
	FileKindVCF     FileKind = "vcf"
	FileKindGVCF    FileKind = "gvcf"
)

type ConversionMode string

const (
	ModeVariantVCF               ConversionMode = "variant_vcf"
	ModeGVCFGenotypeThenLift     ConversionMode = "gvcf_genotype_then_lift"
	ModeGVCFCandidateVariants    ConversionMode = "gvcf_candidate_variants"
	ModeGVCFCalledVariants       ConversionMode = ModeGVCFCandidateVariants // deprecated development alias
	ModeGVCFPreserveExperimental ConversionMode = "gvcf_preserve_experimental"
)

type GVCFDialect string

const (
	GVCFDialectUnknown     GVCFDialect = "unknown"
	GVCFDialectGATK        GVCFDialect = "gatk_non_ref"
	GVCFDialectDeepVariant GVCFDialect = "deepvariant_star"
	GVCFDialectStar        GVCFDialect = "star_placeholder"
	GVCFDialectMixed       GVCFDialect = "mixed_placeholders"
)

type ContigStyle string

const (
	ContigStyleUnknown ContigStyle = "unknown"
	ContigStyleUCSC    ContigStyle = "ucsc"
	ContigStyleGRCh    ContigStyle = "grch"
	ContigStyleMixed   ContigStyle = "mixed"
)

type Assembly string

const (
	AssemblyUnknown Assembly = "unknown"
	AssemblyHG38    Assembly = "hg38"
	AssemblyHG19    Assembly = "hg19"
)

type Direction string

const (
	DirectionForward Direction = "hg38_to_hg19"
	DirectionReverse Direction = "hg19_to_hg38"
)

type Inspection struct {
	Path               string
	Kind               FileKind
	Assembly           Assembly
	ContigStyle        ContigStyle
	ContigNames        []string // header contig order, for reference-coverage checks
	InputIndexPath     string
	InputIndexKind     string
	Samples            []string
	HasNonRefAllele    bool
	HasStarAllele      bool
	GVCFDialect        GVCFDialect
	DeepVariantVersion string
	HasGVCFBlocks      bool
	HasEndInfo         bool
	HasReferenceBlock  bool
	HeaderReference    string
	NumberGFormatTags  []string // FORMAT fields declared Number=G (GT excluded)
	Notes              []string
}

type Plan struct {
	Inspection   Inspection
	Mode         ConversionMode
	Direction    Direction
	OutputPath   string
	Warnings     []string
	Experimental bool
}

type Stage string

const (
	StagePreparing        Stage = "preparing"
	StageInspecting       Stage = "inspecting"
	StageResources        Stage = "resources"
	StageRuntime          Stage = "runtime"
	StageSourceValidation Stage = "source_validation"
	StageLiftover         Stage = "liftover"
	StageGVCFValidation   Stage = "gvcf_validation"
	StageGenotyping       Stage = "genotyping"
	StageTargetValidation Stage = "target_validation"
	StageSorting          Stage = "sorting"
	StageIndexing         Stage = "indexing"
	StageQC               Stage = "qc"
	StageComplete         Stage = "complete"
)

type ProgressEvent struct {
	Stage   Stage
	Item    string
	Message string
	Current int64
	Total   int64
	// Elapsed is set only by heartbeat events that report a still-running
	// pipeline group; zero on ordinary step events.
	Elapsed time.Duration
}

type JobConfig struct {
	InputPath         string
	OutputPath        string
	Mode              ConversionMode
	Direction         Direction // derived from inspection; forward is the default
	TargetProfile     string    // profile.TargetProfile value; "" = ucsc-hg19 default
	GRCh37FASTA       string    // optional second REF check for GRCh37 naming profiles
	Threads           int
	KeepRejected      bool
	PreserveSource    bool
	NeedsSourceRename bool
	ReuseInputIndex   bool
	Overwrite         bool
	// StripFormatTags removes these FORMAT fields (bcftools annotate -x)
	// before source validation when set. Used by the one-shot retry after
	// the pinned liftover plugin rejects ploidy-aware cardinality (e.g.
	// haploid FORMAT/GP on chrX/chrY) that valid VCF 4.2 producers emit.
	StripFormatTags []string
	// DropContigsFile is a targets file (name\tfrom\tto per line) listing
	// input contigs absent from the source reference FASTA. Records on them
	// cannot be REF-validated or lifted; the stream filter removes them and
	// the converter routes them to the rejected-variant bucket. Tab-separated
	// regions are required because contig names may contain ':'.
	DropContigsFile string
}

type Result struct {
	InputKind                   FileKind
	Mode                        ConversionMode
	Direction                   Direction `json:",omitempty"`
	TargetProfile               string    `json:",omitempty"`
	Warnings                    []string  `json:",omitempty"`
	InputRecords                int64
	InputVariants               int64  `json:",omitempty"` // retained for compatibility; ordinary VCF only
	SourceNonRefCalls           int64  `json:",omitempty"`
	SourceNonRefPASSCalls       int64  `json:",omitempty"`
	SourceCandidateVariants     int64  `json:",omitempty"`
	PreprocessingExcluded       *int64 `json:",omitempty"`
	LiftoverInputVariants       int64
	LiftedVariants              int64
	RejectedVariants            int64
	ProfileRejects              map[string]int64 `json:",omitempty"`
	OutputStarAlleleRecords     int64
	OutputNonRefAlleleRecords   int64
	CandidateConservationPassed *bool            `json:",omitempty"`
	Ledger                      *LedgerSummary   `json:",omitempty"`
	Certificate                 *CertificateInfo `json:",omitempty"`
	OutputPath                  string
	IndexPath                   string
	RejectPath                  string
	ProfileRejectPath           string    `json:",omitempty"`
	ReportPath                  string
	StartedAt                   time.Time
	CompletedAt                 time.Time
}

// LedgerSummary carries the conversion ledger counts (docs/LEDGER.md) plus
// the sidecar path for GUI and CLI surfaces.
type LedgerSummary struct {
	ledger.Counts
	SidecarPath string `json:"sidecar_path,omitempty"`
}

// CertificateInfo carries the user-FASTA dictionary verdict
// (docs/CERTIFICATE.md). Statement is the exact verdict sentence block.
type CertificateInfo struct {
	Verdict     string `json:"verdict"`
	Profile     string `json:"profile"`
	FirstReason string `json:"first_reason,omitempty"`
	Statement   string `json:"statement"`
}
