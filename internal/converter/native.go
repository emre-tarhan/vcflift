package converter

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/emre-tarhan/vcflift/internal/engine"
	"github.com/emre-tarhan/vcflift/internal/enginebundle"
	"github.com/emre-tarhan/vcflift/internal/gatk"
	"github.com/emre-tarhan/vcflift/internal/model"
	"github.com/emre-tarhan/vcflift/internal/report"
	"github.com/emre-tarhan/vcflift/internal/resources"
	"github.com/emre-tarhan/vcflift/internal/vcf"
)

const Version = "1.0.0"

type NativeConverter struct {
	Resources    *resources.Manager
	Manifest     resources.Manifest
	Installation engine.Installation
	EngineBundle *enginebundle.Manager
	GATK         gatk.Installation
	GATKManager  *gatk.Manager
	Runner       engine.Runner
}

func NewNative(cacheRoot string) *NativeConverter {
	return &NativeConverter{
		Resources:    resources.NewManager(filepath.Join(cacheRoot, "resources", "v1")),
		Manifest:     resources.DefaultManifest(),
		Installation: engine.ResolveExplicitInstallation(),
		EngineBundle: enginebundle.NewManager(filepath.Join(cacheRoot, "engine")),
		GATK:         gatk.ResolveInstallation(),
		GATKManager:  gatk.NewManager(filepath.Join(cacheRoot, "runtime", "v1")),
	}
}

func (c *NativeConverter) Convert(ctx context.Context, cfg model.JobConfig, progress func(model.ProgressEvent)) (*model.Result, error) {
	started := time.Now().UTC()
	if progress != nil {
		progress(model.ProgressEvent{Stage: model.StageInspecting, Message: "Inspecting input"})
	}
	plan, err := InspectAndPlan(cfg.InputPath)
	if err != nil {
		return nil, err
	}
	if cfg.Mode == "" {
		cfg.Mode = plan.Mode
	}
	if cfg.OutputPath == "" {
		cfg.OutputPath = plan.OutputPath
	}
	if !modeCompatible(plan.Inspection.Kind, cfg.Mode) {
		return nil, fmt.Errorf("requested mode %q is incompatible with detected input %q", cfg.Mode, plan.Inspection.Kind)
	}
	cfg.NeedsSourceRename = plan.Inspection.ContigStyle == model.ContigStyleGRCh

	if err := ensureOutputWritable(cfg.OutputPath, cfg.Overwrite); err != nil {
		return nil, err
	}
	if progress != nil {
		progress(model.ProgressEvent{Stage: model.StagePreparing, Message: "Checking native engine"})
	}
	engineState, err := c.ResolveEngine(ctx)
	if err != nil {
		return nil, err
	}
	inst := engineState.Installation
	engineSource := engineState.Source
	var embeddedManifest enginebundle.Manifest
	if engineState.Manifest != nil {
		embeddedManifest = *engineState.Manifest
	}

	if cfg.Mode == model.ModeGVCFGenotypeThenLift && !cfg.NeedsSourceRename && plan.Inspection.InputIndexPath != "" {
		if progress != nil {
			progress(model.ProgressEvent{Stage: model.StageIndexing, Message: "Validating existing gVCF index"})
		}
		if vcf.SidecarIndexFresh(cfg.InputPath, plan.Inspection.InputIndexPath) && engine.ValidateVariantIndex(ctx, inst.BCFTools, cfg.InputPath) {
			cfg.ReuseInputIndex = true
			plan.Warnings = append(plan.Warnings, "existing "+plan.Inspection.InputIndexKind+" input index was validated and reused")
		} else {
			plan.Warnings = append(plan.Warnings, "adjacent input index could not be validated; VCF Lift rebuilt a temporary index")
		}
	}

	var gatkVersion, gatkSource, javaVersion string
	if cfg.Mode == model.ModeGVCFGenotypeThenLift {
		if progress != nil {
			progress(model.ProgressEvent{Stage: model.StagePreparing, Message: "Checking GATK GenotypeGVCFs runtime"})
		}
		ginst, gatkErr := gatk.Validate(ctx, c.GATK)
		if errors.Is(gatkErr, gatk.ErrNotFound) {
			if c.GATKManager == nil {
				return nil, fmt.Errorf("GATK GenotypeGVCFs is required for gVCF input and no runtime manager is configured")
			}
			if progress != nil {
				progress(model.ProgressEvent{Stage: model.StageRuntime, Message: "Installing pinned Java 17 + GATK runtime for gVCF input"})
			}
			ginst, gatkErr = c.GATKManager.Prepare(ctx, progress)
			if gatkErr == nil {
				ginst, gatkErr = gatk.Validate(ctx, ginst)
			}
		}
		if gatkErr != nil {
			return nil, gatkErr
		}
		c.GATK = ginst
		gatkVersion = ginst.Version
		gatkSource = ginst.Source
		javaVersion = ginst.JavaVersion
	}

	if progress != nil {
		progress(model.ProgressEvent{Stage: model.StageResources, Message: "Preparing reference resources"})
	}
	prepared, err := c.Resources.Prepare(ctx, c.Manifest, progress)
	if err != nil {
		return nil, err
	}

	renameMap := filepath.Join(prepared.Root, "hg38.alias-to-ucsc.tsv")
	if cfg.NeedsSourceRename {
		if _, err := os.Stat(renameMap); errors.Is(err, os.ErrNotExist) {
			if err := resources.BuildRenameMap(prepared.HG38Aliases, renameMap); err != nil {
				return nil, fmt.Errorf("build chromosome alias map: %w", err)
			}
		}
	}

	if err := os.MkdirAll(filepath.Dir(cfg.OutputPath), 0o755); err != nil {
		return nil, err
	}
	tempOutput := cfg.OutputPath + ".partial"
	rejectPath := rejectPathFor(cfg.OutputPath)
	for _, p := range []string{tempOutput, tempOutput + ".tbi"} {
		_ = os.Remove(p)
	}
	if cfg.Overwrite {
		for _, p := range []string{cfg.OutputPath, cfg.OutputPath + ".tbi", reportPathFor(cfg.OutputPath)} {
			_ = os.Remove(p)
		}
	}

	tc := engine.Toolchain{
		BCFTools: inst.BCFTools, PluginDir: inst.PluginDir,
		Java: c.GATK.Java, GATKJar: c.GATK.Jar,
		HG38FASTA: prepared.HG38FASTA, HG19FASTA: prepared.HG19FASTA,
		Chain38To19: prepared.Chain, SourceRenameMap: renameMap,
	}
	pipeline, err := engine.BuildPipeline(tc, cfg, rejectPath, tempOutput)
	if err != nil {
		return nil, err
	}
	cleanupTemps := func() {
		for _, p := range pipeline.TempFiles {
			_ = os.Remove(p)
		}
	}
	defer cleanupTemps()
	if err := c.Runner.Run(ctx, pipeline, progress); err != nil {
		_ = os.Remove(tempOutput)
		_ = os.Remove(tempOutput + ".tbi")
		return nil, err
	}
	if _, err := os.Stat(tempOutput); err != nil {
		return nil, fmt.Errorf("pipeline completed without output: %w", err)
	}
	if _, err := os.Stat(tempOutput + ".tbi"); err != nil {
		return nil, fmt.Errorf("pipeline completed without tabix index: %w", err)
	}

	var sourceCalls vcf.SourceCallSummary
	var inputRecords int64
	if cfg.Mode == model.ModeGVCFCandidateVariants {
		if progress != nil {
			progress(model.ProgressEvent{Stage: model.StageQC, Message: "summarize source candidate calls"})
		}
		sourceCalls, err = vcf.SummarizeSourceCallsBCFTools(ctx, inst.BCFTools, cfg.InputPath, cfg.Threads)
		if err != nil {
			_ = os.Remove(tempOutput)
			_ = os.Remove(tempOutput + ".tbi")
			return nil, fmt.Errorf("summarize source non-reference calls: %w", err)
		}
		inputRecords = sourceCalls.Records
	} else {
		inputRecords, err = vcf.CountRecords(cfg.InputPath)
		if err != nil {
			_ = os.Remove(tempOutput)
			_ = os.Remove(tempOutput + ".tbi")
			return nil, fmt.Errorf("count input records: %w", err)
		}
	}

	if progress != nil {
		progress(model.ProgressEvent{Stage: model.StageQC, Message: "verify output and rejected-variant QC"})
	}
	outputSummary, err := vcf.SummarizeVariantFile(tempOutput, false)
	if err != nil {
		_ = os.Remove(tempOutput)
		_ = os.Remove(tempOutput + ".tbi")
		return nil, fmt.Errorf("summarize output variants: %w", err)
	}
	if cfg.Mode == model.ModeGVCFCandidateVariants && (outputSummary.StarAlleleRecords != 0 || outputSummary.NonRefAlleleRecords != 0) {
		_ = os.Remove(tempOutput)
		_ = os.Remove(tempOutput + ".tbi")
		return nil, fmt.Errorf("candidate-mode output still contains reference-confidence placeholder alleles (<*> records=%d, <NON_REF> records=%d)", outputSummary.StarAlleleRecords, outputSummary.NonRefAlleleRecords)
	}

	var rejectSummary vcf.VariantFileSummary
	if _, statErr := os.Stat(rejectPath); statErr == nil {
		rejectSummary, err = vcf.SummarizeVariantFile(rejectPath, true)
		if err != nil {
			_ = os.Remove(tempOutput)
			_ = os.Remove(tempOutput + ".tbi")
			return nil, fmt.Errorf("summarize rejected variants: %w", err)
		}
	}

	lifted := outputSummary.Records
	rejected := rejectSummary.Records
	liftoverInput := lifted + rejected
	var candidateConservation *bool
	if cfg.Mode == model.ModeGVCFCandidateVariants {
		ok := sourceCalls.LiftoverCandidates == liftoverInput
		candidateConservation = &ok
		if !ok {
			plan.Warnings = append(plan.Warnings, fmt.Sprintf("candidate conservation mismatch: source non-reference calls=%d, lifted+rejected=%d", sourceCalls.LiftoverCandidates, liftoverInput))
		}
	}

	if err := finalizePair(tempOutput, cfg.OutputPath); err != nil {
		return nil, err
	}

	reportPath := reportPathFor(cfg.OutputPath)
	completed := time.Now().UTC()
	doc := report.Document{
		Tool: "VCF Lift", ToolVersion: Version,
		SourceAssembly: "hg38", TargetAssembly: "hg19",
		OutputClass: report.OutputClassVariantVCF,
		InputKind:    plan.Inspection.Kind, Mode: cfg.Mode,
		InputRecords: inputRecords, LiftoverInputVariants: liftoverInput,
		LiftedVariants: lifted, RejectedVariants: rejected,
		StartedAt: started, CompletedAt: completed,
		Resources: report.ResourceInfo{
			SourceReference: filepath.Base(prepared.HG38FASTA),
			TargetReference: filepath.Base(prepared.HG19FASTA),
			Chain:           filepath.Base(prepared.Chain),
		},
		Engine: report.EngineInfo{
			Source:          engineSource,
			BCFToolsVersion: inst.Version,
			ScoreRef:        embeddedManifest.ScoreRef,
			GATKVersion:     gatkVersion,
			GATKSource:      gatkSource,
			JavaVersion:     javaVersion,
		},
		Warnings: plan.Warnings,
	}
	if cfg.Mode == model.ModeGVCFCandidateVariants {
		doc.SourceNonRefCalls = sourceCalls.NonReferenceCalls
		doc.SourceNonRefPASSCalls = sourceCalls.NonReferencePASS
		doc.SourceCandidateVariants = sourceCalls.LiftoverCandidates
		if sourceCalls.NonReferenceCalls >= sourceCalls.LiftoverCandidates {
			excluded := sourceCalls.NonReferenceCalls - sourceCalls.LiftoverCandidates
			doc.PreprocessingExcluded = &excluded
		}
	}
	if plan.Inspection.Kind == model.FileKindGVCF {
		// Contract metric: source gVCF records the variant-VCF output does
		// not carry (reference-confidence territory).
		dropped := inputRecords - liftoverInput
		doc.GVCFBlocksDropped = &dropped
	}
	doc.QC = report.QCInfo{
		SourceRefValidationPassed:   true,
		TargetRefValidationPassed:   true,
		OutputIndexCreated:          true,
		OutputStarAlleleRecords:     outputSummary.StarAlleleRecords,
		OutputNonRefAlleleRecords:   outputSummary.NonRefAlleleRecords,
		CandidateConservationPassed: candidateConservation,
	}
	if rejectSummary.Records > 0 {
		doc.RejectSummary = &report.RejectSummary{
			Filters: rejectSummary.FilterCounts,
			Reasons: rejectSummary.RejectReasonCounts,
			Contigs: rejectSummary.ContigCounts,
		}
	}

	if plan.Inspection.InputIndexPath != "" {
		doc.InputIndex = &report.InputIndexInfo{
			Path: filepath.Base(plan.Inspection.InputIndexPath), Kind: plan.Inspection.InputIndexKind, Reused: cfg.ReuseInputIndex,
		}
	}
	if err := report.Write(reportPath, doc); err != nil {
		return nil, fmt.Errorf("write report: %w", err)
	}

	result := &model.Result{
		InputKind: plan.Inspection.Kind, Mode: cfg.Mode,
		InputRecords: inputRecords, LiftoverInputVariants: liftoverInput,
		LiftedVariants: lifted, RejectedVariants: rejected,
		OutputStarAlleleRecords: outputSummary.StarAlleleRecords, OutputNonRefAlleleRecords: outputSummary.NonRefAlleleRecords,
		CandidateConservationPassed: candidateConservation,
		OutputPath:                  cfg.OutputPath, IndexPath: cfg.OutputPath + ".tbi",
		ReportPath: reportPath, StartedAt: started, CompletedAt: completed,
	}
	if cfg.Mode == model.ModeGVCFCandidateVariants {
		result.SourceNonRefCalls = sourceCalls.NonReferenceCalls
		result.SourceNonRefPASSCalls = sourceCalls.NonReferencePASS
		result.SourceCandidateVariants = sourceCalls.LiftoverCandidates
		if sourceCalls.NonReferenceCalls >= sourceCalls.LiftoverCandidates {
			excluded := sourceCalls.NonReferenceCalls - sourceCalls.LiftoverCandidates
			result.PreprocessingExcluded = &excluded
		}
	}
	if plan.Inspection.Kind == model.FileKindVCF {
		result.InputVariants = inputRecords
	}
	if cfg.KeepRejected {
		result.RejectPath = rejectPath
	} else {
		_ = os.Remove(rejectPath)
		_ = os.Remove(rejectPath + ".tbi")
	}
	if progress != nil {
		progress(model.ProgressEvent{Stage: model.StageComplete, Message: "Conversion complete", Current: lifted, Total: lifted + rejected})
	}
	return result, nil
}

func ensureOutputWritable(path string, overwrite bool) error {
	if path == "" {
		return fmt.Errorf("output path is empty")
	}
	if _, err := os.Stat(path); err == nil && !overwrite {
		return fmt.Errorf("output already exists: %s", path)
	}
	return nil
}

func finalizePair(temp, final string) error {
	idxTemp, idxFinal := temp+".tbi", final+".tbi"
	idxStage := idxFinal + ".partial"
	_ = os.Remove(idxStage)
	if err := os.Rename(idxTemp, idxStage); err != nil {
		return fmt.Errorf("stage output index: %w", err)
	}
	if err := os.Rename(temp, final); err != nil {
		_ = os.Rename(idxStage, idxTemp)
		return fmt.Errorf("finalize output: %w", err)
	}
	if err := os.Rename(idxStage, idxFinal); err != nil {
		return fmt.Errorf("finalize output index: %w", err)
	}
	return nil
}

func rejectPathFor(output string) string { return output + ".rejected.vcf.gz" }
func reportPathFor(output string) string { return output + ".report.json" }

func modeCompatible(kind model.FileKind, mode model.ConversionMode) bool {
	switch kind {
	case model.FileKindVCF:
		return mode == model.ModeVariantVCF
	case model.FileKindGVCF:
		return mode == model.ModeGVCFGenotypeThenLift || mode == model.ModeGVCFCandidateVariants || mode == model.ModeGVCFPreserveExperimental
	default:
		return false
	}
}
