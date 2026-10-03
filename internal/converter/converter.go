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
// default conversion mode. hg38 inputs convert forward to hg19; hg19/GRCh37
// inputs convert in reverse to hg38. GATK-style gVCFs are genotyped on the
// source hg38 assembly before variant liftover. Merely stripping <NON_REF> is
// retained only as an explicit advanced/candidate-sites mode, not as the
// default final-call path.
func InspectAndPlan(path string) (model.Plan, error) {
	inspection, err := vcf.Inspect(path)
	if err != nil {
		return model.Plan{}, err
	}
	if inspection.ContigStyle == model.ContigStyleMixed {
		return model.Plan{Inspection: inspection}, fmt.Errorf("mixed chromosome naming detected")
	}
	if inspection.Assembly == model.AssemblyHG19 {
		return planReverse(path, inspection)
	}

	plan := model.Plan{Inspection: inspection, Direction: model.DirectionForward}
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
		plan.OutputPath = vcf.DefaultOutputPath(path, plan.Mode, plan.Direction)
		return plan, nil
	}

	plan.Mode = model.ModeVariantVCF
	plan.OutputPath = vcf.DefaultOutputPath(path, plan.Mode, plan.Direction)
	return plan, nil
}

// planReverse plans hg19/GRCh37 → hg38 conversion. The managed GATK runtime
// carries only an hg38 reference, so GATK-style gVCFs must be genotyped by the
// user against their own hg19 reference before conversion.
func planReverse(path string, inspection model.Inspection) (model.Plan, error) {
	plan := model.Plan{Inspection: inspection, Direction: model.DirectionReverse}
	plan.Warnings = append(plan.Warnings,
		"hg19/GRCh37 input detected: conversion runs in reverse (hg19 → hg38)",
	)
	if inspection.Kind == model.FileKindGVCF {
		switch inspection.GVCFDialect {
		case model.GVCFDialectDeepVariant, model.GVCFDialectStar:
			plan.Mode = model.ModeGVCFCandidateVariants
			plan.Warnings = append(plan.Warnings,
				"<*>-style gVCF detected: finalized variant calls are extracted, then lifted to hg38",
				"reference-confidence blocks are omitted from the hg38 variant VCF",
			)
		case model.GVCFDialectGATK:
			return plan, fmt.Errorf("GATK-style gVCF on hg19/GRCh37: run GenotypeGVCFs against your own hg19 reference first, then convert the resulting variant VCF (the managed runtime is hg38-only)")
		default:
			return plan, fmt.Errorf("gVCF placeholder dialect is ambiguous (%s); choose an explicit --gvcf-mode after reviewing the file", inspection.GVCFDialect)
		}
	} else {
		plan.Mode = model.ModeVariantVCF
	}
	plan.OutputPath = vcf.DefaultOutputPath(path, plan.Mode, plan.Direction)
	return plan, nil
}
