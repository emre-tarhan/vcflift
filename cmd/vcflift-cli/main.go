package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/emre-tarhan/vcflift/internal/cache"
	"github.com/emre-tarhan/vcflift/internal/certificate"
	"github.com/emre-tarhan/vcflift/internal/converter"
	"github.com/emre-tarhan/vcflift/internal/engine"
	"github.com/emre-tarhan/vcflift/internal/enginebundle"
	"github.com/emre-tarhan/vcflift/internal/gatk"
	"github.com/emre-tarhan/vcflift/internal/model"
	"github.com/emre-tarhan/vcflift/internal/paths"
	"github.com/emre-tarhan/vcflift/internal/rejects"
	"github.com/emre-tarhan/vcflift/internal/resources"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "inspect":
		err = runInspect(os.Args[2:])
	case "convert":
		err = runConvert(os.Args[2:])
	case "resources":
		err = runResources(os.Args[2:])
	case "runtime":
		err = runRuntime(os.Args[2:])
	case "engine":
		err = runEngine(os.Args[2:])
	case "cache":
		err = runCache(os.Args[2:])
	case "reject-audit":
		err = runRejectAudit(os.Args[2:])
	case "version", "--version", "-version":
		fmt.Println(converter.Version)
		return
	case "help", "--help", "-h":
		usage()
		return
	default:
		// Backward-compatible shorthand: `vcflift-cli sample.vcf.gz` inspects.
		err = runInspect(os.Args[1:])
	}
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		var es exitStatus
		if errors.As(err, &es) {
			fmt.Fprintln(os.Stderr, es.message)
			os.Exit(es.code)
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		if errors.Is(err, resources.ErrLicenseAcceptanceRequired) {
			fmt.Fprintln(os.Stderr, "hint: review UCSC license terms and rerun with --accept-ucsc-license")
		}
		os.Exit(1)
	}
}

// exitStatus reports a completed run whose requested check did not pass:
// outputs and report were written, the process still exits non-zero with the
// verdict sentence. Code 3 stays distinct from engine failures (1) and usage
// errors (2).
type exitStatus struct {
	code    int
	message string
}

func (e exitStatus) Error() string { return e.message }

func usage() {
	fmt.Fprintln(os.Stderr, `VCF Lift

Usage:
  vcflift-cli inspect <input.vcf.gz>
  vcflift-cli convert [options] <input.vcf.gz>
  vcflift-cli resources [options]
  vcflift-cli runtime [options]
  vcflift-cli engine check [options]
  vcflift-cli engine import [options]
  vcflift-cli engine install --bundle <engine.zip>
  vcflift-cli engine status [options]
  vcflift-cli cache status [options]
  vcflift-cli cache clean [options]
  vcflift-cli reject-audit <rejected.vcf.gz>

GATK-style <NON_REF> gVCFs are genotyped on hg38 before liftover. DeepVariant/<*> gVCFs preserve the source caller's finalized variant calls and trim reference-confidence blocks before liftover.`)
}

func runInspect(args []string) error {
	fs := flag.NewFlagSet("inspect", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("inspect requires one input VCF/gVCF")
	}
	plan, err := converter.InspectAndPlan(fs.Arg(0))
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(plan)
}

func runConvert(args []string) error {
	fs := flag.NewFlagSet("convert", flag.ContinueOnError)
	var output, cache, bcftools, pluginDir, javaPath, gatkJar, gvcfMode, targetProfile, grch37FASTA string
	var threads int
	var keepRejects, overwrite, acceptUCSC bool
	fs.StringVar(&output, "output", "", "output .vcf.gz path")
	fs.StringVar(&cache, "cache", paths.CacheDir(), "VCF Lift cache root")
	fs.StringVar(&bcftools, "bcftools", "", "bcftools executable (defaults to bundled/PATH lookup)")
	fs.StringVar(&pluginDir, "plugin-dir", "", "BCFTOOLS_PLUGINS directory")
	fs.StringVar(&javaPath, "java", "", "Java executable for GATK gVCF genotyping")
	fs.StringVar(&gatkJar, "gatk-jar", "", "GATK package jar for GenotypeGVCFs")
	fs.StringVar(&gvcfMode, "gvcf-mode", "auto", "gVCF mode: auto (recommended), genotype, or candidate")
	fs.StringVar(&targetProfile, "target-profile", "ucsc-hg19", "target naming profile: ucsc-hg19 (default), grch37-primary, hs37d5 (forward hg38 to hg19), or grch38-primary (reverse hg19 to hg38)")
	fs.StringVar(&grch37FASTA, "grch37-fasta", "", "optional GRCh37 FASTA for a second REF check (grch37-primary/hs37d5 profiles only)")
	fs.IntVar(&threads, "threads", 0, "bcftools worker threads (0 = tool default)")
	fs.BoolVar(&keepRejects, "keep-rejects", false, "keep rejected variants VCF")
	fs.BoolVar(&overwrite, "overwrite", false, "replace existing output")
	fs.BoolVar(&acceptUCSC, "accept-ucsc-license", false, "confirm you reviewed/accept the UCSC chain-file license terms")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("convert requires one input VCF/gVCF")
	}

	c := converter.NewNative(cache)
	if bcftools != "" {
		c.Installation.BCFTools = bcftools
	}
	if pluginDir != "" {
		c.Installation.PluginDir = pluginDir
	}
	if javaPath != "" {
		c.GATK.Java = javaPath
	}
	if gatkJar != "" {
		c.GATK.Jar = gatkJar
	}
	c.Resources.AcceptRestrictedData = acceptUCSC

	cfg := model.JobConfig{InputPath: fs.Arg(0), OutputPath: output, TargetProfile: targetProfile, GRCh37FASTA: grch37FASTA, Threads: threads, KeepRejected: keepRejects, Overwrite: overwrite}
	switch gvcfMode {
	case "", "auto":
		// InspectAndPlan picks the path for the detected gVCF dialect.
	case "genotype":
		cfg.Mode = model.ModeGVCFGenotypeThenLift
	case "candidate":
		cfg.Mode = model.ModeGVCFCandidateVariants
	default:
		return fmt.Errorf("unknown --gvcf-mode %q (expected auto, genotype, or candidate)", gvcfMode)
	}
	result, err := c.Convert(context.Background(), cfg, func(e model.ProgressEvent) {
		switch {
		case e.Elapsed > 0:
			fmt.Fprintf(os.Stderr, "[%s] %s — still running (%s elapsed)\n", e.Stage, e.Message, e.Elapsed.Round(time.Second))
		case e.Total > 0:
			fmt.Fprintf(os.Stderr, "[%s] %s (%d/%d)\n", e.Stage, e.Message, e.Current, e.Total)
		default:
			fmt.Fprintf(os.Stderr, "[%s] %s\n", e.Stage, e.Message)
		}
	})
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(result); err != nil {
		return err
	}
	// Dictionary certificate (docs/CERTIFICATE.md Section 7): the conversion
	// completed and every output stands; the exit code is non-zero because
	// the verdict is incompatible, not because a check was skipped.
	if result.Certificate != nil && result.Certificate.Verdict == certificate.VerdictIncompatible {
		return exitStatus{code: 3, message: result.Certificate.Statement + "\n" + certificate.StateIncompatible}
	}
	return nil
}

func runResources(args []string) error {
	fs := flag.NewFlagSet("resources", flag.ContinueOnError)
	cache := fs.String("cache", paths.CacheDir(), "VCF Lift cache root")
	accept := fs.Bool("accept-ucsc-license", false, "confirm you reviewed/accept the UCSC chain-file license terms")
	if err := fs.Parse(args); err != nil {
		return err
	}
	m := resources.NewManager(filepath.Join(*cache, "resources", "v1"))
	m.AcceptRestrictedData = *accept
	prepared, err := m.Prepare(context.Background(), resources.DefaultManifest(), func(e model.ProgressEvent) {
		if e.Total > 0 {
			fmt.Fprintf(os.Stderr, "%s: %d/%d\n", e.Message, e.Current, e.Total)
		} else {
			fmt.Fprintln(os.Stderr, e.Message)
		}
	})
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(prepared)
}

func runRuntime(args []string) error {
	fs := flag.NewFlagSet("runtime", flag.ContinueOnError)
	cache := fs.String("cache", paths.CacheDir(), "VCF Lift cache root")
	if err := fs.Parse(args); err != nil {
		return err
	}
	m := gatk.NewManager(filepath.Join(*cache, "runtime", "v1"))
	inst, err := m.Prepare(context.Background(), func(e model.ProgressEvent) {
		if e.Total > 0 {
			fmt.Fprintf(os.Stderr, "[%s] %s (%d/%d)\n", e.Stage, e.Message, e.Current, e.Total)
		} else {
			fmt.Fprintf(os.Stderr, "[%s] %s\n", e.Stage, e.Message)
		}
	})
	if err != nil {
		return err
	}
	inst, err = gatk.Validate(context.Background(), inst)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(inst)
}

func runRejectAudit(args []string) error {
	fs := flag.NewFlagSet("reject-audit", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("reject-audit requires one rejected VCF")
	}
	a, err := rejects.Analyze(fs.Arg(0))
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(a)
}

func runCache(args []string) error {
	if len(args) == 0 {
		args = []string{"status"}
	}
	switch args[0] {
	case "status":
		fs := flag.NewFlagSet("cache status", flag.ContinueOnError)
		root := fs.String("cache", paths.CacheDir(), "VCF Lift cache root")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		st, err := cache.Inspect(*root)
		if err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(st)
	case "clean":
		fs := flag.NewFlagSet("cache clean", flag.ContinueOnError)
		root := fs.String("cache", paths.CacheDir(), "VCF Lift cache root")
		all := fs.Bool("all", false, "remove the entire VCF Lift cache (references and runtimes will need to be downloaded again)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *all {
			if err := cache.ClearAll(*root); err != nil {
				return err
			}
			fmt.Println(`{"cleared_all":true}`)
			return nil
		}
		res, err := cache.CleanSafe(*root)
		if err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	default:
		return fmt.Errorf("unknown cache command %q (expected status or clean)", args[0])
	}
}

func runEngine(args []string) error {
	if len(args) == 0 {
		return runEngineCheck(nil)
	}
	switch args[0] {
	case "check":
		return runEngineCheck(args[1:])
	case "import":
		return runEngineImport(args[1:])
	case "install":
		return runEngineInstall(args[1:])
	case "status":
		return runEngineStatus(args[1:])
	default:
		// Backward compatible: `engine --bcftools ...` still means check.
		return runEngineCheck(args)
	}
}

func runEngineCheck(args []string) error {
	fs := flag.NewFlagSet("engine check", flag.ContinueOnError)
	bcftools := fs.String("bcftools", "", "bcftools executable (defaults to explicit/PATH lookup)")
	pluginDir := fs.String("plugin-dir", "", "BCFTOOLS_PLUGINS directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	inst := engine.ResolveInstallation()
	if *bcftools != "" {
		inst.BCFTools = *bcftools
	}
	if *pluginDir != "" {
		inst.PluginDir = *pluginDir
	}
	validated, err := engine.ValidateInstallation(context.Background(), inst)
	if err != nil {
		return err
	}
	if err := engine.RequireVersion(validated, engine.RequiredBCFToolsVersion); err != nil {
		return err
	}
	if err := engine.SmokeTest(context.Background(), validated); err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(validated)
}

func runEngineImport(args []string) error {
	fs := flag.NewFlagSet("engine import", flag.ContinueOnError)
	cache := fs.String("cache", paths.CacheDir(), "VCF Lift cache root")
	bcftools := fs.String("bcftools", "", "path to bcftools 1.24 executable")
	pluginDir := fs.String("plugin-dir", "", "directory containing liftover plugin")
	scoreRef := fs.String("score-ref", "local-unverified", "freeseek/score commit/tag if known")
	bundleOut := fs.String("bundle-out", "", "portable engine bundle zip (default current directory)")
	runtimeDirs := fs.String("runtime-dirs", "", "comma-separated directories containing runtime DLL/SO files")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *bcftools == "" || *pluginDir == "" {
		return fmt.Errorf("engine import requires --bcftools and --plugin-dir")
	}
	if *bundleOut == "" {
		*bundleOut = filepath.Join(".", "vcflift-engine-"+enginebundle.PlatformKey()+".zip")
	}
	var extra []string
	for _, item := range strings.Split(*runtimeDirs, ",") {
		if item = strings.TrimSpace(item); item != "" {
			extra = append(extra, item)
		}
	}
	validatedLocal, err := engine.ValidateInstallation(context.Background(), engine.Installation{BCFTools: *bcftools, PluginDir: *pluginDir})
	if err != nil {
		return err
	}
	if err := engine.RequireVersion(validatedLocal, engine.RequiredBCFToolsVersion); err != nil {
		return err
	}
	if err := engine.SmokeTest(context.Background(), validatedLocal); err != nil {
		return err
	}
	packed, err := enginebundle.PackLocal(context.Background(), enginebundle.PackOptions{
		Installation: engine.Installation{BCFTools: *bcftools, PluginDir: *pluginDir},
		OutputZip:    *bundleOut,
		ScoreRef:     *scoreRef,
		RuntimeDirs:  extra,
	})
	if err != nil {
		return err
	}
	manager := enginebundle.NewManager(filepath.Join(*cache, "engine"))
	installed, manifest, err := manager.InstallArchive(packed.Archive)
	if err != nil {
		return err
	}
	installed, err = engine.ValidateInstallation(context.Background(), installed)
	if err != nil {
		return fmt.Errorf("installed engine failed preflight: %w", err)
	}
	if err := engine.RequireVersion(installed, engine.RequiredBCFToolsVersion); err != nil {
		return err
	}
	result := struct {
		Bundle       string                `json:"bundle"`
		Installation engine.Installation   `json:"installation"`
		Manifest     enginebundle.Manifest `json:"manifest"`
	}{Bundle: packed.Archive, Installation: installed, Manifest: manifest}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}

func runEngineInstall(args []string) error {
	fs := flag.NewFlagSet("engine install", flag.ContinueOnError)
	cache := fs.String("cache", paths.CacheDir(), "VCF Lift cache root")
	bundle := fs.String("bundle", "", "portable VCF Lift engine bundle zip")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *bundle == "" {
		return fmt.Errorf("engine install requires --bundle")
	}
	manager := enginebundle.NewManager(filepath.Join(*cache, "engine"))
	inst, manifest, err := manager.InstallArchive(*bundle)
	if err != nil {
		return err
	}
	inst, err = engine.ValidateInstallation(context.Background(), inst)
	if err != nil {
		return fmt.Errorf("installed engine failed preflight: %w", err)
	}
	if err := engine.RequireVersion(inst, engine.RequiredBCFToolsVersion); err != nil {
		return err
	}
	if err := engine.SmokeTest(context.Background(), inst); err != nil {
		return err
	}
	result := struct {
		Installation engine.Installation   `json:"installation"`
		Manifest     enginebundle.Manifest `json:"manifest"`
	}{Installation: inst, Manifest: manifest}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}

func runEngineStatus(args []string) error {
	fs := flag.NewFlagSet("engine status", flag.ContinueOnError)
	cache := fs.String("cache", paths.CacheDir(), "VCF Lift cache root")
	if err := fs.Parse(args); err != nil {
		return err
	}
	manager := enginebundle.NewManager(filepath.Join(*cache, "engine"))
	type status struct {
		Source       string                 `json:"source"`
		Installation engine.Installation    `json:"installation"`
		Manifest     *enginebundle.Manifest `json:"manifest,omitempty"`
	}

	// Explicit override has highest priority.
	if explicit := engine.ResolveExplicitInstallation(); explicit.BCFTools != "" {
		inst, err := engine.ValidateInstallation(context.Background(), explicit)
		if err != nil {
			return err
		}
		if err := engine.RequireVersion(inst, engine.RequiredBCFToolsVersion); err != nil {
			return err
		}
		return writeJSON(status{Source: "explicit", Installation: inst})
	}
	// In release builds this activates the embedded pinned engine. Development
	// builds report ErrBundleUnavailable and continue to an imported bundle.
	if inst, manifest, err := manager.Install(); err == nil {
		validated, err := engine.ValidateInstallation(context.Background(), inst)
		if err != nil {
			return err
		}
		return writeJSON(status{Source: "embedded", Installation: validated, Manifest: &manifest})
	} else if !errors.Is(err, enginebundle.ErrBundleUnavailable) {
		return err
	}
	if inst, manifest, err := manager.FindInstalled(); err == nil {
		validated, err := engine.ValidateInstallation(context.Background(), inst)
		if err != nil {
			return err
		}
		return writeJSON(status{Source: "installed_bundle", Installation: validated, Manifest: &manifest})
	} else if !errors.Is(err, enginebundle.ErrBundleUnavailable) {
		return err
	}
	if pathInst := engine.ResolvePathInstallation(); pathInst.BCFTools != "" {
		validated, err := engine.ValidateInstallation(context.Background(), pathInst)
		if err != nil {
			return err
		}
		if err := engine.RequireVersion(validated, engine.RequiredBCFToolsVersion); err != nil {
			return err
		}
		return writeJSON(status{Source: "system_path", Installation: validated})
	}
	return engine.ErrEngineNotFound
}

func writeJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
