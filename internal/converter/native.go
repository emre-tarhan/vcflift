package converter

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/emre-tarhan/vcflift/internal/engine"
	"github.com/emre-tarhan/vcflift/internal/enginebundle"
	"github.com/emre-tarhan/vcflift/internal/gatk"
	"github.com/emre-tarhan/vcflift/internal/model"
	"github.com/emre-tarhan/vcflift/internal/profile"
	"github.com/emre-tarhan/vcflift/internal/report"
	"github.com/emre-tarhan/vcflift/internal/resources"
	"github.com/emre-tarhan/vcflift/internal/vcf"
)

const Version = "1.1.0"

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
	if cfg.Direction == "" {
		cfg.Direction = plan.Direction
	}
	if cfg.Direction == "" {
		cfg.Direction = model.DirectionForward
	}
	if !modeCompatible(plan.Inspection.Kind, cfg.Mode) {
		return nil, fmt.Errorf("requested mode %q is incompatible with detected input %q", cfg.Mode, plan.Inspection.Kind)
	}
	if cfg.Mode == model.ModeGVCFGenotypeThenLift && cfg.Direction == model.DirectionReverse {
		return nil, fmt.Errorf("GATK GenotypeGVCFs is hg38-only; genotype hg19 gVCFs externally before reverse conversion")
	}
	prof, err := profile.Parse(cfg.TargetProfile)
	if err != nil {
		return nil, err
	}
	cfg.TargetProfile = string(prof)
	if cfg.Direction == model.DirectionReverse && !prof.IsDefault() {
		return nil, fmt.Errorf("target naming profiles apply to the forward hg38 to hg19 direction only")
	}
	if cfg.GRCh37FASTA != "" {
		if !prof.RenamesToGRCh37() {
			return nil, fmt.Errorf("grch37 FASTA second REF check requires the grch37-primary or hs37d5 target profile")
		}
		if _, statErr := os.Stat(cfg.GRCh37FASTA); statErr != nil {
			return nil, fmt.Errorf("grch37 FASTA: %w", statErr)
		}
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
	aliasSource := prepared.HG38Aliases
	if cfg.Direction == model.DirectionReverse {
		renameMap = filepath.Join(prepared.Root, "hg19.alias-to-ucsc.tsv")
		aliasSource = prepared.HG19Aliases
	}
	if cfg.NeedsSourceRename {
		if _, err := os.Stat(renameMap); errors.Is(err, os.ErrNotExist) {
			if err := resources.BuildRenameMap(aliasSource, renameMap); err != nil {
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
		for _, p := range []string{cfg.OutputPath, cfg.OutputPath + ".tbi", reportPathFor(cfg.OutputPath), profileRejectPathFor(cfg.OutputPath), profileRejectPathFor(cfg.OutputPath) + ".tbi"} {
			_ = os.Remove(p)
		}
	}

	tc := engine.Toolchain{
		BCFTools: inst.BCFTools, PluginDir: inst.PluginDir,
		Java: c.GATK.Java, GATKJar: c.GATK.Jar,
		HG38FASTA: prepared.HG38FASTA, HG19FASTA: prepared.HG19FASTA,
		Chain38To19: prepared.Chain, Chain19To38: prepared.Chain19To38,
		SourceRenameMap: renameMap,
	}
	pipeline, err := engine.BuildPipeline(tc, cfg, rejectPath, tempOutput)
	if err != nil {
		return nil, err
	}
	var profileTemps []string
	cleanupTemps := func() {
		for _, p := range pipeline.TempFiles {
			_ = os.Remove(p)
		}
		for _, p := range profileTemps {
			_ = os.Remove(p)
			_ = os.Remove(p + ".tbi")
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

	// Target naming profiles run after the validated liftover pipeline: the
	// lifted UCSC hg19 output is renamed/filtered without touching the core.
	profileRejectPath := profileRejectPathFor(cfg.OutputPath)
	var profileStats profile.Stats
	if prof.RenamesToGRCh37() {
		if progress != nil {
			progress(model.ProgressEvent{Stage: model.StageQC, Message: "apply " + string(prof) + " target profile"})
		}
		profileStats, err = c.applyProfile(ctx, inst.BCFTools, cfg, prof, tempOutput, profileRejectPath, &profileTemps, progress)
		if err != nil {
			_ = os.Remove(tempOutput)
			_ = os.Remove(tempOutput + ".tbi")
			cleanupTemps()
			return nil, err
		}
		tempOutput = tempOutput + ".profiled.vcf.gz"
		if prof == profile.HS37D5 {
			plan.Warnings = append(plan.Warnings, "hs37d5 naming profile: output uses GRCh37 primary contig naming; hs37d5 decoy contigs are not produced")
		}
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
	// Conservation math must describe the liftover itself, so profile-dropped
	// records count as lifted here.
	preProfileLifted := lifted
	if prof.RenamesToGRCh37() {
		preProfileLifted = profileStats.PreProfileRecords()
	}
	liftoverInput := preProfileLifted + rejected
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
	sourceAssembly, targetAssembly := "hg38", "hg19"
	if cfg.Direction == model.DirectionReverse {
		sourceAssembly, targetAssembly = "hg19", "hg38"
	}
	doc := report.Document{
		Tool: "VCF Lift", ToolVersion: Version,
		SourceAssembly: sourceAssembly, TargetAssembly: targetAssembly,
		OutputClass: report.OutputClassVariantVCF,
		InputKind:   plan.Inspection.Kind, Mode: cfg.Mode,
		TargetProfile: string(prof),
		InputRecords:  inputRecords, LiftoverInputVariants: liftoverInput,
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
	if pr := profileStats.ProfileRejects(); pr != nil {
		doc.ProfileRejects = pr
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
		Direction:     cfg.Direction,
		TargetProfile: string(prof),
		InputRecords:  inputRecords, LiftoverInputVariants: liftoverInput,
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
	if profileStats.StaleChrM > 0 || profileStats.NonPrimaryContig > 0 {
		result.ProfileRejects = profileStats.ProfileRejects()
		if cfg.KeepRejected {
			result.ProfileRejectPath = profileRejectPath
		} else {
			_ = os.Remove(profileRejectPath)
			_ = os.Remove(profileRejectPath + ".tbi")
		}
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

func profileRejectPathFor(output string) string { return output + ".profile-rejected.vcf.gz" }

// applyProfile splits the lifted UCSC-hg19 output into the GRCh37-named
// primary output plus a profile reject bucket, then compresses both through
// the native engine. It appends its temporary files to temps.
func (c *NativeConverter) applyProfile(ctx context.Context, bcftoolsPath string, cfg model.JobConfig, prof profile.Profile, liftedOutput, profileRejectPath string, temps *[]string, progress func(model.ProgressEvent)) (profile.Stats, error) {
	var stats profile.Stats
	primaryPlain := liftedOutput + ".grch37.primary.vcf"
	rejectPlain := liftedOutput + ".profile-rejected.vcf"
	profiled := liftedOutput + ".profiled.vcf.gz"
	*temps = append(*temps, primaryPlain, rejectPlain, profiled, profiled+".tbi")

	cleanup := func() {
		for _, p := range []string{primaryPlain, rejectPlain, profiled, profiled + ".tbi", profileRejectPath, profileRejectPath + ".tbi"} {
			_ = os.Remove(p)
		}
	}

	in, closeIn, err := profile.OpenText(liftedOutput)
	if err != nil {
		cleanup()
		return stats, err
	}
	pf, err := os.Create(primaryPlain)
	if err != nil {
		closeIn()
		cleanup()
		return stats, err
	}
	rf, err := os.Create(rejectPlain)
	if err != nil {
		closeIn()
		_ = pf.Close()
		cleanup()
		return stats, err
	}
	stats, err = profile.Split(in, prof, pf, rf)
	closeIn()
	_ = pf.Close()
	_ = rf.Close()
	if err != nil {
		cleanup()
		return stats, fmt.Errorf("apply %s target profile: %w", prof, err)
	}

	// Optional second REF check against a user-provided GRCh37 FASTA. Done in
	// Go (faidx comparison) because `bcftools norm -N -c e` skips the REF
	// check when normalization is disabled, and norm without -N would rewrite
	// the validated representation.
	if cfg.GRCh37FASTA != "" {
		if err := profile.CheckREF(primaryPlain, cfg.GRCh37FASTA); err != nil {
			cleanup()
			return stats, err
		}
	}

	threadArgs := []string{}
	if cfg.Threads > 0 {
		threadArgs = []string{"--threads", strconv.Itoa(cfg.Threads)}
	}
	steps := []engine.Step{}
	viewArgs := []string{"view", "-Oz", "-o", profiled}
	viewArgs = append(viewArgs, threadArgs...)
	viewArgs = append(viewArgs, primaryPlain)
	steps = append(steps, engine.Step{Name: "apply " + string(prof) + " target profile", Executable: bcftoolsPath, Args: viewArgs, PipeToNext: false})
	steps = append(steps, engine.Step{Name: "index profiled output", Executable: bcftoolsPath, Args: []string{"index", "--tbi", "--force", profiled}, PipeToNext: false})
	if stats.StaleChrM > 0 || stats.NonPrimaryContig > 0 {
		steps = append(steps, engine.Step{Name: "write profile reject bucket", Executable: bcftoolsPath, Args: []string{"view", "-Oz", "-o", profileRejectPath, rejectPlain}, PipeToNext: false})
	}
	pl := engine.Pipeline{Mode: cfg.Mode, Env: map[string]string{}, Steps: steps, TempFiles: []string{primaryPlain, rejectPlain}}
	if err := c.Runner.Run(ctx, pl, progress); err != nil {
		cleanup()
		return stats, err
	}
	return stats, nil
}

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
