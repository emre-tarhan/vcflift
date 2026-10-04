package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/emre-tarhan/vcflift/internal/certificate"
	"github.com/emre-tarhan/vcflift/internal/ledger"
	"github.com/emre-tarhan/vcflift/internal/model"
)

type EngineInfo struct {
	Source          string `json:"source"`
	BCFToolsVersion string `json:"bcftools_version"`
	ScoreRef        string `json:"score_ref,omitempty"`
	GATKVersion     string `json:"gatk_version,omitempty"`
	GATKSource      string `json:"gatk_source,omitempty"`
	JavaVersion     string `json:"java_version,omitempty"`
}

type ResourceInfo struct {
	SourceReference string `json:"source_reference"`
	TargetReference string `json:"target_reference"`
	Chain           string `json:"chain"`
}

type InputIndexInfo struct {
	Path   string `json:"path,omitempty"`
	Kind   string `json:"kind,omitempty"`
	Reused bool   `json:"reused"`
}

type QCInfo struct {
	SourceRefValidationPassed   bool  `json:"source_ref_validation_passed"`
	TargetRefValidationPassed   bool  `json:"target_ref_validation_passed"`
	OutputIndexCreated          bool  `json:"output_index_created"`
	OutputStarAlleleRecords     int64 `json:"output_star_allele_records"`
	OutputNonRefAlleleRecords   int64 `json:"output_non_ref_allele_records"`
	CandidateConservationPassed *bool `json:"candidate_conservation_passed,omitempty"`
}

type RejectSummary struct {
	Filters map[string]int64 `json:"filters,omitempty"`
	Reasons map[string]int64 `json:"reasons,omitempty"`
	Contigs map[string]int64 `json:"contigs,omitempty"`
}

// OutputClassVariantVCF is the deliverable contract for every conversion
// mode: the output is a variant VCF. gVCF reference-confidence territory is
// never carried into the output.
const OutputClassVariantVCF = "variant_vcf"

type Document struct {
	Tool                    string               `json:"tool"`
	ToolVersion             string               `json:"tool_version"`
	SourceAssembly          string               `json:"source_assembly"`
	TargetAssembly          string               `json:"target_assembly"`
	OutputClass             string               `json:"output_class"`
	InputKind               model.FileKind       `json:"input_kind"`
	Mode                    model.ConversionMode `json:"mode"`
	TargetProfile           string               `json:"target_profile"`
	InputRecords            int64                `json:"input_records"`
	SourceNonRefCalls       int64                `json:"source_nonref_calls,omitempty"`
	SourceNonRefPASSCalls   int64                `json:"source_nonref_pass_calls,omitempty"`
	SourceCandidateVariants int64                `json:"source_candidate_variants,omitempty"`
	PreprocessingExcluded   *int64               `json:"preprocessing_excluded,omitempty"`
	GVCFBlocksDropped       *int64               `json:"gvcf_blocks_dropped,omitempty"`
	LiftoverInputVariants   int64                `json:"liftover_input_variants"`
	LiftedVariants          int64                `json:"lifted_variants"`
	RejectedVariants        int64                `json:"rejected_variants"`
	ProfileRejects          map[string]int64     `json:"profile_rejects,omitempty"`
	StartedAt               time.Time            `json:"started_at"`
	CompletedAt             time.Time            `json:"completed_at"`
	Resources               ResourceInfo         `json:"resources"`
	Engine                  EngineInfo           `json:"engine"`
	InputIndex              *InputIndexInfo      `json:"input_index,omitempty"`
	QC                      QCInfo               `json:"qc"`
	RejectSummary           *RejectSummary       `json:"reject_summary,omitempty"`
	Ledger                  *ledger.Counts       `json:"ledger,omitempty"`
	LedgerSidecar           string               `json:"ledger_sidecar,omitempty"`
	FastaCertificate        *certificate.Report  `json:"fasta_certificate,omitempty"`
	Warnings                []string             `json:"warnings,omitempty"`
}

func Write(path string, doc Document) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
