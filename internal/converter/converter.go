package converter

import (
	"context"
	"fmt"

	"github.com/emre-tarhan/vcflift/internal/model"
	"github.com/emre-tarhan/vcflift/internal/vcf"
)

type Converter interface {
	Convert(context.Context, model.JobConfig, func(model.ProgressEvent)) (*model.Result, error)
}

// InspectAndPlan classifies the input and chooses a scientifically conservative
// default conversion mode. GATK-style gVCFs are genotyped on the source hg38
// assembly before variant liftover. Merely stripping <NON_REF> is retained only
// as an explicit advanced/candidate-sites mode, not as the default final-call path.
func InspectAndPlan(path string) (model.Plan, error) {
	inspection, err := vcf.Inspect(path)
	if err != nil {
		return model.Plan{}, err
	}
	if inspection.ContigStyle == model.ContigStyleMixed {
		return model.Plan{Inspection: inspection}, fmt.Errorf("mixed chromosome naming detected")
	}
	if inspection.Assembly == model.AssemblyHG19 {
		return model.Plan{Inspection: inspection}, fmt.Errorf("input appears to already use hg19/GRCh37 coordinates")
	}

	plan := model.Plan{Inspection: inspection}
	if inspection.Kind == model.FileKindGVCF {
		switch inspection.GVCFDialect {
		case model.GVCFDialectDeepVariant, model.GVCFDialectStar:
			// DeepVariant and other <*>-placeholder gVCFs already contain the
			// source caller's finalized variant records. GATK GenotypeGVCFs
			// requires <NON_REF> and is not the appropriate default for these
			// files. Extract concrete called variants, trim the unseen <*>
			// allele and allele-dependent fields, then perform allele-aware
			// liftover.
			plan.Mode = model.ModeGVCFCandidateVariants
			plan.Warnings = append(plan.Warnings,
				"<*>-style gVCF detected: preserving the source caller's finalized variant calls instead of running GATK GenotypeGVCFs",
				"reference-confidence blocks are omitted from the hg19 variant VCF",
			)
		case model.GVCFDialectGATK:
			plan.Mode = model.ModeGVCFGenotypeThenLift
			plan.Warnings = append(plan.Warnings,
				"GATK-style <NON_REF> gVCF detected: source-assembly genotyping runs before hg38 to hg19 liftover",
				"reference-confidence blocks are not presented as a native hg19 gVCF",
			)
		default:
			return model.Plan{Inspection: inspection}, fmt.Errorf("gVCF placeholder dialect is ambiguous (%s); choose an explicit --gvcf-mode after reviewing the file", inspection.GVCFDialect)
		}
		plan.OutputPath = vcf.DefaultOutputPath(path, plan.Mode)
		return plan, nil
	}

	plan.Mode = model.ModeVariantVCF
	plan.OutputPath = vcf.DefaultOutputPath(path, plan.Mode)
	return plan, nil
}
