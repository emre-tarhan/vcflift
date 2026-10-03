package engine

import (
	"fmt"
	"strconv"

	"github.com/emre-tarhan/vcflift/internal/model"
)

type Toolchain struct {
	BCFTools        string
	PluginDir       string
	Java            string
	GATKJar         string
	HG38FASTA       string
	HG19FASTA       string
	Chain38To19     string
	SourceRenameMap string
}

type Step struct {
	Name       string
	Executable string
	Args       []string
	PipeToNext bool
}

type Pipeline struct {
	Mode      model.ConversionMode
	Env       map[string]string
	Steps     []Step
	TempFiles []string
}

// BuildPipeline constructs the native-tool pipeline but does not execute it.
// All shell metacharacters are avoided; the runner connects
// stdout/stdin with os/exec pipes so the same plan works on Windows and Linux.
func BuildPipeline(tc Toolchain, cfg model.JobConfig, rejectPath, tempOutput string) (Pipeline, error) {
	if tc.BCFTools == "" || tc.HG38FASTA == "" || tc.HG19FASTA == "" || tc.Chain38To19 == "" {
		return Pipeline{}, fmt.Errorf("incomplete native toolchain")
	}
	if cfg.InputPath == "" || tempOutput == "" {
		return Pipeline{}, fmt.Errorf("input and temporary output paths are required")
	}
	if cfg.NeedsSourceRename && tc.SourceRenameMap == "" {
		return Pipeline{}, fmt.Errorf("source chromosome rename map is required")
	}
	if cfg.Mode == model.ModeGVCFPreserveExperimental {
		return Pipeline{}, fmt.Errorf("gVCF reference-block preservation requires the block-aware preprocessor and is not enabled yet")
	}
	if cfg.Mode == model.ModeGVCFGenotypeThenLift && (tc.Java == "" || tc.GATKJar == "") {
		return Pipeline{}, fmt.Errorf("GATK GenotypeGVCFs runtime is required for the default gVCF conversion mode")
	}

	p := Pipeline{Mode: cfg.Mode, Env: map[string]string{}}
	if tc.PluginDir != "" {
		p.Env["BCFTOOLS_PLUGINS"] = tc.PluginDir
	}
	threads := cfg.Threads
	if threads < 0 {
		threads = 0
	}
	threadArgs := []string{}
	if threads > 0 {
		threadArgs = []string{"--threads", strconv.Itoa(threads)}
	}

	// GATK gVCFs are intermediate reference-confidence files. The conservative
	// path first validates/materializes the source gVCF against hg38, then runs
	// GenotypeGVCFs on hg38, and only then lifts the resulting ordinary VCF.
	if cfg.Mode == model.ModeGVCFGenotypeThenLift {
		sourceGVCF := tempOutput + ".source.hg38.g.vcf.gz"
		genotypedVCF := tempOutput + ".genotyped.hg38.vcf.gz"
		p.TempFiles = append(p.TempFiles, genotypedVCF, genotypedVCF+".tbi")

		gvcfForGATK := cfg.InputPath
		if cfg.ReuseInputIndex && !cfg.NeedsSourceRename {
			// Keep the user's original gVCF byte-for-byte. -N turns off the
			// left-alignment/normalization that -f would otherwise enable, so
			// this step is a REF-consistency check only. GenotypeGVCFs can then
			// reuse the already validated adjacent .tbi/.csi directly.
			validateArgs := []string{"norm", "-N", "-f", tc.HG38FASTA, "-c", "e", "-Ou"}
			validateArgs = append(validateArgs, threadArgs...)
			validateArgs = append(validateArgs, cfg.InputPath)
			p.Steps = append(p.Steps, Step{Name: "validate indexed hg38 gVCF", Executable: tc.BCFTools, Args: validateArgs, PipeToNext: false})
		} else if cfg.NeedsSourceRename {
			p.TempFiles = append(p.TempFiles, sourceGVCF, sourceGVCF+".tbi", sourceGVCF+".csi")
			renameArgs := []string{"annotate", "--rename-chrs", tc.SourceRenameMap, "-Ou"}
			renameArgs = append(renameArgs, threadArgs...)
			renameArgs = append(renameArgs, cfg.InputPath)
			p.Steps = append(p.Steps, Step{Name: "normalize gVCF chromosome names", Executable: tc.BCFTools, Args: renameArgs, PipeToNext: true})

			normArgs := []string{"norm", "-N", "-f", tc.HG38FASTA, "-c", "e", "-Oz", "-o", sourceGVCF}
			normArgs = append(normArgs, threadArgs...)
			p.Steps = append(p.Steps, Step{Name: "validate and stage hg38 gVCF", Executable: tc.BCFTools, Args: normArgs, PipeToNext: false})
			p.Steps = append(p.Steps, Step{Name: "index staged hg38 gVCF", Executable: tc.BCFTools, Args: []string{"index", "--tbi", "--force", sourceGVCF}, PipeToNext: false})
			gvcfForGATK = sourceGVCF
		} else {
			p.TempFiles = append(p.TempFiles, sourceGVCF, sourceGVCF+".tbi", sourceGVCF+".csi")
			normArgs := []string{"norm", "-N", "-f", tc.HG38FASTA, "-c", "e", "-Oz", "-o", sourceGVCF}
			normArgs = append(normArgs, threadArgs...)
			normArgs = append(normArgs, cfg.InputPath)
			p.Steps = append(p.Steps, Step{Name: "validate and stage hg38 gVCF", Executable: tc.BCFTools, Args: normArgs, PipeToNext: false})
			p.Steps = append(p.Steps, Step{Name: "index staged hg38 gVCF", Executable: tc.BCFTools, Args: []string{"index", "--tbi", "--force", sourceGVCF}, PipeToNext: false})
			gvcfForGATK = sourceGVCF
		}
		p.Steps = append(p.Steps,
			Step{Name: "genotype hg38 gVCF", Executable: tc.Java, Args: []string{
				"-Xmx4g", "-jar", tc.GATKJar, "GenotypeGVCFs",
				"-R", tc.HG38FASTA,
				"-V", gvcfForGATK,
				"-O", genotypedVCF,
				"--create-output-variant-index", "false",
			}, PipeToNext: false},
		)

		srcArgs := []string{"norm", "-f", tc.HG38FASTA, "-c", "e", "-Ou"}
		srcArgs = append(srcArgs, threadArgs...)
		srcArgs = append(srcArgs, genotypedVCF)
		p.Steps = append(p.Steps, Step{Name: "validate genotyped hg38 variants", Executable: tc.BCFTools, Args: srcArgs, PipeToNext: true})
		appendLiftoverTail(&p, tc, threadArgs, rejectPath, tempOutput)
		return p, nil
	}

	inputAlreadyPiped := false
	if cfg.NeedsSourceRename {
		args := []string{"annotate", "--rename-chrs", tc.SourceRenameMap, "-Ou"}
		args = append(args, threadArgs...)
		args = append(args, cfg.InputPath)
		p.Steps = append(p.Steps, Step{Name: "normalize chromosome names", Executable: tc.BCFTools, Args: args, PipeToNext: true})
		inputAlreadyPiped = true
	}

	if cfg.Mode == model.ModeGVCFCandidateVariants {
		args := []string{
			"view",
			"-A",
			"-a",
			"-i", `GT="alt"`,
			"-Ou",
		}
		args = append(args, threadArgs...)
		if !inputAlreadyPiped {
			args = append(args, cfg.InputPath)
		}
		p.Steps = append(p.Steps, Step{Name: "extract candidate variant records from gVCF", Executable: tc.BCFTools, Args: args, PipeToNext: true})
		inputAlreadyPiped = true
	}

	// Validate source REF after any chromosome renaming / gVCF allele trimming.
	srcArgs := []string{"norm", "-f", tc.HG38FASTA, "-c", "e", "-Ou"}
	srcArgs = append(srcArgs, threadArgs...)
	if !inputAlreadyPiped {
		srcArgs = append(srcArgs, cfg.InputPath)
	}
	p.Steps = append(p.Steps, Step{Name: "validate hg38 reference alleles", Executable: tc.BCFTools, Args: srcArgs, PipeToNext: true})
	appendLiftoverTail(&p, tc, threadArgs, rejectPath, tempOutput)
	return p, nil
}

func appendLiftoverTail(p *Pipeline, tc Toolchain, threadArgs []string, rejectPath, tempOutput string) {
	liftoverArgs := []string{"+liftover", "-Ou"}
	liftoverArgs = append(liftoverArgs, threadArgs...)
	liftoverArgs = append(liftoverArgs,
		"--",
		"-s", tc.HG38FASTA,
		"-f", tc.HG19FASTA,
		"-c", tc.Chain38To19,
		"--reject", rejectPath,
		"--reject-type", "z",
		"--write-src",
		"--write-reject",
	)
	p.Steps = append(p.Steps, Step{Name: "allele-aware liftover", Executable: tc.BCFTools, Args: liftoverArgs, PipeToNext: true})

	targetArgs := []string{"norm", "-f", tc.HG19FASTA, "-c", "e", "-Ou"}
	targetArgs = append(targetArgs, threadArgs...)
	p.Steps = append(p.Steps, Step{Name: "validate hg19 reference alleles", Executable: tc.BCFTools, Args: targetArgs, PipeToNext: true})

	sortArgs := []string{"sort", "-Oz", "-o", tempOutput}
	p.Steps = append(p.Steps,
		Step{Name: "sort and compress", Executable: tc.BCFTools, Args: sortArgs, PipeToNext: false},
		Step{Name: "index", Executable: tc.BCFTools, Args: []string{"index", "--tbi", "--force", tempOutput}, PipeToNext: false},
	)
}
