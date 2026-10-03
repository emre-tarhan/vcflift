# Desktop GUI

VCF Lift's desktop app is a focused local conversion workspace.

## Visual direction

The GUI is **light-mode only** for v1.0. It does not inherit a dark OS palette. The visual system uses a broken-white canvas (`#F1F3F6`), white cards with hairline borders (`#DCE2E8`), a readable ink/slate text ramp (titles `#182430`, body `#2E3C49`, secondary `#5C6C7B`) and one restrained steel-blue accent (`#35618F`, soft wash `#E4EDF6`), with muted success/warning/error colors.

A deliberate theme detail: Fyne renders `LowImportance` labels with the theme's *disabled* color, so the theme overrides `ColorNameDisabled` with a readable slate (`#6B7B89`) instead of the near-invisible default. Never use `LowImportance` for text that must be more prominent than secondary; use `canvas.Text` with an explicit palette color for small caps/meta labels.

Button styling rules that follow from Fyne's renderer: `ColorNameButton` is a soft gray chip (`#EFF2F5`) so medium-importance buttons stay visible on white cards; `ColorNameHover`/`ColorNamePressed` must remain **translucent** overlays because Fyne alpha-blends them over the button background — an opaque value repaints primary (blue) buttons near-white and hides their white label text.

The goal is a scientific desktop utility with clear contrast and low ambiguity. Technical engine details should not compete with the user's conversion task.

## Convert

The primary path is:

```text
choose or drop an hg38 or hg19 VCF/gVCF
  -> inspect detected format/assembly/index/sample count
  -> review the automatic output path (hg19 for hg38 input, hg38 for hg19 input)
  -> convert
  -> review result and QC report
```

Conversion progress is **stage-based**, not a fake linear percentage. The macro stages are:

1. Prepare input
2. Convert variants
3. Create index
4. Quality checks

The native pipeline streams source validation, allele-aware liftover, target validation and BGZF writing concurrently, so presenting those subprocesses as independent percentages would be misleading. The GUI groups them into the "Convert variants" stage while keeping detailed CLI/log events available for diagnostics.

## First-run setup

On startup the app checks the local reference profile. If required resources are absent, a setup dialog opens automatically and explains that:

- hg38 and hg19 references are required;
- the UCSC liftOver chains (both directions) and chromosome aliases will be downloaded;
- downloads are resumable and checksum-verified;
- processing remains local;
- the chains are subject to UCSC terms.

Starting setup switches to the Resources view and begins preparation. A user may dismiss the dialog, but conversion will still require the missing resources and will prompt before downloading the UCSC chain.

## Resources

Reference resources are shown as rows, each with the resource title and short description on the left and a right-aligned two-line status stack (download state over prepare state) on the right:

```text
hg38 reference                    NOT READY
Source reference genome                WAITING
```

States are `NOT READY / DOWNLOADING / VERIFIED` for download and `WAITING / PREPARING / READY / NOT REQUIRED` for prepare. Download progress bars are per resource. FASTA decompression/index/dictionary work appears as `PREPARING` only after the verified download phase; this avoids the old UI appearing to be stuck on "Downloading hg38/hg19" while CPU/disk preparation was actually running.

When all references are ready, the large setup call-to-action disappears in favor of a small `Check & repair` action. Users should not be left wondering whether they must download the same references again.

The native bcftools/liftover engine is read-only in the normal GUI and described as **included with VCF Lift**. The old `Install development engine bundle` control has been removed from end-user UI. Development engine staging belongs to source/build tooling, not normal application setup.

## Storage and trust

Resources shows cache path, used space and free space. Safe cleanup removes temporary/archive leftovers while keeping prepared references/runtimes. Full cache reset remains an explicit destructive action.

The main conversion screen includes a visible local-processing trust signal. VCF Lift has no telemetry and does not upload genotype data.

## Visual QA gate

The GUI compiles locally with `./build.sh --dev --gui` (seconds on a warm Go cache). Under WSLg the app opens on the Wayland backend and is invisible to X11 capture tools; screenshot review therefore happens in a real desktop session with manual captures. Before v1.0, perform Linux and Windows visual smoke tests at common scaling levels and verify:

- contrast/readability;
- modal sizing;
- resource rows during real parallel download and FASTA preparation;
- conversion stage transitions during a real large file;
- long file/path wrapping;
- success/error/cancel states;
- HiDPI/Windows scaling.
