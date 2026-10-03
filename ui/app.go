package ui

import (
	"context"
	"fmt"
	"image/color"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	cachepkg "github.com/emre-tarhan/vcflift/internal/cache"
	"github.com/emre-tarhan/vcflift/internal/converter"
	"github.com/emre-tarhan/vcflift/internal/diskspace"
	"github.com/emre-tarhan/vcflift/internal/model"
	"github.com/emre-tarhan/vcflift/internal/paths"
	"github.com/emre-tarhan/vcflift/internal/resources"
)

func Run() {
	a := app.NewWithID("org.vcflift.desktop")
	a.Settings().SetTheme(newGenomeTheme())
	w := a.NewWindow("VCF Lift")
	w.Resize(fyne.NewSize(980, 760))

	header := buildHeader()
	cachePath := paths.CacheDir()
	manifest := resources.DefaultManifest()
	resourceRoot := filepath.Join(cachePath, "resources", "v1")
	resourceManager := resources.NewManager(resourceRoot)

	// ---- Conversion workspace ----
	fileName := mutedLabel("No file selected")
	fileName.Wrapping = fyne.TextWrapBreak
	kind := valueLabel("—")
	assembly := valueLabel("—")
	contigs := valueLabel("—")
	index := valueLabel("—")
	samples := valueLabel("—")
	output := mutedLabel("Output is chosen automatically next to the input file.")
	output.Wrapping = fyne.TextWrapBreak

	statusTitle := widget.NewLabelWithStyle("Ready for a file", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	statusBody := mutedLabel("Choose an hg38 VCF or supported gVCF. Everything is processed locally on this computer.")
	statusBody.Wrapping = fyne.TextWrapWord
	workflow, workflowView := newWorkflowTracker()

	var selectedPath string
	var currentPlan model.Plan
	var cancel context.CancelFunc

	convertBtn := widget.NewButtonWithIcon("Convert to hg19", theme.NavigateNextIcon(), nil)
	convertBtn.Importance = widget.HighImportance
	convertBtn.Disable()
	cancelBtn := widget.NewButton("Cancel", func() {
		if cancel != nil {
			cancel()
		}
	})
	cancelBtn.Hide()

	setBusy := func(busy bool) {
		if busy {
			convertBtn.Disable()
			cancelBtn.Show()
			return
		}
		cancelBtn.Hide()
		if selectedPath != "" {
			convertBtn.Enable()
		}
	}

	setStatus := func(title, body string) {
		statusTitle.SetText(title)
		statusBody.SetText(body)
	}

	// Declared before the inspectors so they can reveal it once a file is
	// known; the card itself is assembled further below.
	var metadataSection fyne.CanvasObject

	inspectPath := func(path string) {
		workflow.Reset()
		plan, err := converter.InspectAndPlan(path)
		currentPlan = plan
		inspection := plan.Inspection
		selectedPath = path
		fileName.SetText(filepath.Base(path))
		if err != nil {
			setStatus("Input needs attention", err.Error())
			convertBtn.Disable()
			if inspection.Kind != model.FileKindUnknown {
				kind.SetText(displayKind(inspection.Kind))
				metadataSection.Show()
			}
			return
		}
		kind.SetText(displayKind(inspection.Kind))
		assembly.SetText(strings.ToUpper(string(inspection.Assembly)))
		contigs.SetText(string(inspection.ContigStyle))
		if inspection.InputIndexPath != "" {
			index.SetText(strings.ToUpper(inspection.InputIndexKind) + " detected")
		} else if inspection.Kind == model.FileKindGVCF {
			index.SetText("Temporary index will be created")
		} else {
			index.SetText("Not required")
		}
		samples.SetText(fmt.Sprintf("%d", len(inspection.Samples)))
		output.SetText(plan.OutputPath)
		metadataSection.Show()
		if inspection.Kind == model.FileKindGVCF {
			const gvcfOutputContract = "The output will be an hg19 variant VCF, not an hg19 gVCF — joint genotyping cannot be done on hg19."
			switch plan.Mode {
			case model.ModeGVCFCandidateVariants:
				setStatus("DeepVariant gVCF ready", "Finalized non-reference calls will be kept. "+gvcfOutputContract)
			case model.ModeGVCFGenotypeThenLift:
				setStatus("GATK gVCF ready", "GenotypeGVCFs will run against hg38 first. The pinned Java and GATK runtime is prepared automatically when needed. "+gvcfOutputContract)
			default:
				setStatus("gVCF ready", "VCF Lift selected the appropriate source preparation path for this gVCF. "+gvcfOutputContract)
			}
		} else {
			setStatus("VCF ready", "REF alleles will be checked against hg38 before conversion and against hg19 after conversion.")
		}
		convertBtn.Enable()
	}

	browse := widget.NewButtonWithIcon("Choose file", theme.FolderOpenIcon(), func() {
		fd := dialog.NewFileOpen(func(f fyne.URIReadCloser, err error) {
			if err != nil {
				dialog.ShowError(err, w)
				return
			}
			if f == nil {
				return
			}
			path := f.URI().Path()
			_ = f.Close()
			inspectPath(path)
		}, w)
		fd.SetFilter(storage.NewExtensionFileFilter([]string{".vcf", ".gz"}))
		fd.Show()
	})
	browse.Importance = widget.HighImportance

	updateProgress := func(e model.ProgressEvent) {
		workflow.Update(e)
		if e.Stage == model.StageComplete {
			setStatus("Conversion complete", "The hg19 VCF, index and QC report are ready.")
			return
		}
		setStatus(conversionPhaseTitle(e.Stage), conversionPhaseBody(e.Stage))
	}

	runConversion := func() {
		ctx, c := context.WithCancel(context.Background())
		cancel = c
		workflow.Reset()
		workflow.Start()
		setBusy(true)
		setStatus("Preparing input", "Checking the input, local engine and reference data before conversion starts.")

		go func() {
			native := converter.NewNative(cachePath)
			native.Resources.AcceptRestrictedData = true
			cfg := model.JobConfig{InputPath: selectedPath, OutputPath: currentPlan.OutputPath, Mode: currentPlan.Mode, KeepRejected: true}
			result, err := native.Convert(ctx, cfg, func(e model.ProgressEvent) {
				fyne.Do(func() { updateProgress(e) })
			})
			fyne.Do(func() {
				cancel = nil
				setBusy(false)
				if err != nil {
					workflow.Fail()
					setStatus("Conversion stopped", err.Error())
					dialog.ShowError(err, w)
					return
				}
				workflow.Complete()
				total := result.LiftedVariants + result.RejectedVariants
				successRate, rejectRate := 0.0, 0.0
				if total > 0 {
					successRate = 100 * float64(result.LiftedVariants) / float64(total)
					rejectRate = 100 * float64(result.RejectedVariants) / float64(total)
				}
				setStatus("Conversion complete", fmt.Sprintf("%d variants were written to the hg19 output. QC and indexing completed successfully.", result.LiftedVariants))
				dialog.ShowInformation("Conversion complete", fmt.Sprintf(
					"Lifted variants: %d (%.3f%%)\nRejected variants: %d (%.3f%%)\n\nOutput\n%s\n\nQC report\n%s",
					result.LiftedVariants, successRate, result.RejectedVariants, rejectRate, result.OutputPath, result.ReportPath,
				), w)
			})
		}()
	}

	convertBtn.OnTapped = func() {
		if selectedPath == "" {
			return
		}
		if resourceManager.AllReady(manifest) {
			runConversion()
			return
		}
		msg := "Reference data is not ready yet. VCF Lift will download and prepare the required hg38/hg19 data and the UCSC hg38-to-hg19 chain before conversion.\n\nThe chain is subject to the UCSC terms. Continue after reviewing and accepting those terms?\n\nhttps://genome.ucsc.edu/license/"
		dialog.NewConfirm("Prepare required references", msg, func(ok bool) {
			if ok {
				runConversion()
			}
		}, w).Show()
	}

	inputSection := section("1", "Input", "VCF or gVCF · hg38", container.NewBorder(nil, nil, nil, browse, fileName))
	metadataGrid := container.NewGridWithColumns(5,
		stat("Format", kind), stat("Assembly", assembly), stat("Contigs", contigs), stat("Index", index), stat("Samples", samples),
	)
	metadataSection = section("", "Detected file", "Read-only inspection of the selected file", metadataGrid)
	metadataSection.Hide()
	outputSection := section("2", "Output", "BGZF VCF + TBI + QC report", output)
	workflowSection := section("3", "Convert", "Per-stage progress · no fake percentages", container.NewVBox(
		workflowView,
		container.New(layout.NewCustomPaddedLayout(12, 0, 0, 0), container.NewVBox(statusTitle, statusBody)),
		container.New(layout.NewCustomPaddedLayout(14, 0, 0, 0), container.NewHBox(layout.NewSpacer(), cancelBtn, convertBtn)),
	))

	convertContent := container.NewVScroll(container.New(
		layout.NewCustomPaddedLayout(18, 20, 22, 22),
		container.NewVBox(
			sectionIntro("Convert genome build", "Local, allele-aware conversion from hg38 to hg19. Drop a file anywhere in this window or choose it below."),
			spacer(12),
			trustStrip(),
			spacer(12),
			inputSection,
			spacer(10),
			metadataSection,
			spacer(10),
			outputSection,
			spacer(10),
			workflowSection,
		),
	))

	// ---- Resource workspace ----
	cachePathLabel := mutedLabel(cachePath)
	cachePathLabel.Wrapping = fyne.TextWrapBreak
	cachePathLabel.TextStyle = fyne.TextStyle{Monospace: true}
	cacheSize := valueLabel("Checking…")
	cacheFree := valueLabel("Checking…")
	engineStatus := valueLabel("Checking…")
	referenceSummary := valueLabel("Checking reference data…")
	resourceRows := makeResourceRows(manifest)
	resourceList := container.NewVBox()
	for i, r := range manifest.Resources {
		row := resourceRows[r.ID]
		if row == nil {
			continue
		}
		resourceList.Add(row.View)
		if i < len(manifest.Resources)-1 {
			resourceList.Add(widget.NewSeparator())
		}
	}

	prepareResourcesBtn := widget.NewButton("Set up resources", nil)
	var tabs *container.AppTabs
	var resourceBusy bool

	refreshReferenceRows := func() bool {
		statuses := resourceManager.Status(manifest)
		readyCount := 0
		for _, st := range statuses {
			if row := resourceRows[st.ID]; row != nil {
				row.SetReady(st.Ready)
			}
			if st.Ready {
				readyCount++
			}
		}
		allReady := len(statuses) > 0 && readyCount == len(statuses)
		if allReady {
			referenceSummary.SetText("Ready · all reference data is prepared locally")
			prepareResourcesBtn.SetText("Check & repair")
			prepareResourcesBtn.Importance = widget.MediumImportance
		} else {
			referenceSummary.SetText(fmt.Sprintf("Setup required · %d of %d resources ready", readyCount, len(statuses)))
			prepareResourcesBtn.SetText("Set up resources")
			prepareResourcesBtn.Importance = widget.HighImportance
		}
		prepareResourcesBtn.Refresh()
		return allReady
	}

	refreshResourceState := func() {
		cacheSize.SetText("Checking…")
		cacheFree.SetText("Checking…")
		refreshReferenceRows()
		go func() {
			st, _ := cachepkg.Inspect(cachePath)
			free, _ := diskspace.Available(cachePath)
			ctx, c := context.WithTimeout(context.Background(), 8*time.Second)
			defer c()
			native := converter.NewNative(cachePath)
			state, engErr := native.ResolveEngine(ctx)
			fyne.Do(func() {
				cacheSize.SetText(formatBytes(st.TotalBytes))
				cacheFree.SetText(formatBytes(int64(free)))
				if engErr != nil {
					engineStatus.SetText("Engine unavailable · rebuild or reinstall VCF Lift")
				} else {
					engineStatus.SetText(fmt.Sprintf("Included with VCF Lift · %s", state.Installation.Version))
				}
			})
		}()
	}

	startReferencePreparation := func() {
		if resourceBusy {
			return
		}
		resourceBusy = true
		prepareResourcesBtn.Disable()
		referenceSummary.SetText("Preparing reference data…")
		go func() {
			m := resources.NewManager(resourceRoot)
			m.AcceptRestrictedData = true
			_, err := m.Prepare(context.Background(), manifest, func(e model.ProgressEvent) {
				fyne.Do(func() {
					if row := resourceRows[e.Item]; row != nil {
						row.Update(e)
					}
				})
			})
			fyne.Do(func() {
				resourceBusy = false
				prepareResourcesBtn.Enable()
				refreshResourceState()
				if err != nil {
					referenceSummary.SetText("Reference setup needs attention")
					dialog.ShowError(err, w)
					return
				}
				referenceSummary.SetText("Ready · all reference data is prepared locally")
			})
		}()
	}

	requestReferencePreparation := func() {
		if resourceManager.AllReady(manifest) {
			startReferencePreparation()
			return
		}
		content := container.NewVBox(
			widget.NewLabelWithStyle("Reference data is required before conversion", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			mutedLabel("VCF Lift will download hg38, hg19, the hg38-to-hg19 chain and chromosome aliases. Downloads are resumable, checksum-verified and prepared locally."),
			mutedLabel("The UCSC chain is downloaded at runtime and is subject to UCSC terms: https://genome.ucsc.edu/license/"),
		)
		dialog.NewCustomConfirm("Set up VCF Lift", "Start setup", "Not now", content, func(ok bool) {
			if !ok {
				return
			}
			if tabs != nil {
				tabs.SelectIndex(1)
			}
			startReferencePreparation()
		}, w).Show()
	}
	prepareResourcesBtn.OnTapped = requestReferencePreparation

	cleanBtn := widget.NewButton("Clean temporary files", func() {
		res, err := cachepkg.CleanSafe(cachePath)
		if err != nil {
			dialog.ShowError(err, w)
			return
		}
		dialog.ShowInformation("Cache cleaned", fmt.Sprintf("Removed %d files and freed %s. Prepared references and installed runtimes were kept.", res.RemovedFiles, formatBytes(res.FreedBytes)), w)
		refreshResourceState()
	})
	clearBtn := widget.NewButton("Clear all cached data…", func() {
		dialog.NewConfirm("Clear all cached data", "Remove references, runtimes and the extracted native engine? They will need to be prepared again before the next conversion.", func(ok bool) {
			if !ok {
				return
			}
			if err := cachepkg.ClearAll(cachePath); err != nil {
				dialog.ShowError(err, w)
				return
			}
			refreshResourceState()
		}, w).Show()
	})
	clearBtn.Importance = widget.DangerImportance

	resourceHeader := container.NewBorder(nil, nil, container.NewVBox(referenceSummary, mutedLabel("Downloads and preparation are shown separately for each required item.")), prepareResourcesBtn)
	resourcesContent := container.NewVScroll(container.New(
		layout.NewCustomPaddedLayout(18, 20, 22, 22),
		container.NewVBox(
			sectionIntro("Resources", "Required reference data stays on this computer and is reused between conversions."),
			spacer(12),
			section("", "Reference data", "Parallel downloads · resumable · checksum-verified", container.NewVBox(resourceHeader, spacer(10), resourceList)),
			spacer(10),
			section("", "Conversion engine", "No setup is normally required", container.NewVBox(engineStatus, mutedLabel("The native bcftools + liftover engine is embedded in the application and selected automatically for this platform."))),
			spacer(10),
			section("", "Storage", "Cache location and disk space", container.NewVBox(cachePathLabel, spacer(8), container.NewGridWithColumns(2, stat("Used", cacheSize), stat("Free on volume", cacheFree)), spacer(8), container.NewHBox(cleanBtn, clearBtn))),
		),
	))

	about := container.NewVScroll(container.New(
		layout.NewCustomPaddedLayout(18, 20, 22, 22),
		container.NewVBox(
			sectionIntro("About VCF Lift", "Local hg38 to hg19 conversion with explicit validation and auditable rejects."),
			spacer(12),
			surface(container.NewVBox(
				aboutRow("Privacy by design", "VCF and genotype records are processed locally. VCF Lift has no telemetry and does not upload sample data."),
				spacer(12),
				aboutRow("Scientific safeguards", "Source REF validation, allele-aware liftover, target REF validation, output indexing, placeholder checks and machine-readable QC reports are part of every supported conversion path."),
				spacer(12),
				aboutRow("Project", "github.com/emre-tarhan/vcflift"),
				spacer(12),
				aboutRow("Version", converter.Version),
			)),
		),
	))

	tabs = container.NewAppTabs(
		container.NewTabItemWithIcon("Convert", theme.FileTextIcon(), convertContent),
		container.NewTabItemWithIcon("Resources", theme.StorageIcon(), resourcesContent),
		container.NewTabItemWithIcon("About", theme.InfoIcon(), about),
	)
	tabs.SetTabLocation(container.TabLocationTop)

	footer := container.NewVBox(
		hairline(),
		container.New(layout.NewCustomPaddedLayout(8, 10, 18, 18), container.NewHBox(
			canvasLabel("LOCAL PROCESSING", 10, true, palette.Muted),
			layout.NewSpacer(),
			canvasLabel("NO TELEMETRY · NO GENOTYPE UPLOAD", 10, true, palette.Muted),
		)),
	)

	w.SetContent(container.NewBorder(header, footer, nil, nil, tabs))
	w.SetOnDropped(func(_ fyne.Position, uris []fyne.URI) {
		if len(uris) == 0 {
			return
		}
		inspectPath(uris[0].Path())
		tabs.SelectIndex(0)
	})

	initialReady := refreshReferenceRows()
	refreshResourceState()
	w.Show()
	if !initialReady {
		go func() {
			time.Sleep(180 * time.Millisecond)
			fyne.Do(func() {
				content := container.NewVBox(
					widget.NewLabelWithStyle("One-time reference setup", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
					mutedLabel("VCF Lift needs local hg38/hg19 reference data before it can convert files. Setup downloads the required data in parallel, verifies it, and reuses it for future conversions."),
					mutedLabel("The UCSC hg38-to-hg19 chain is downloaded at runtime and is subject to UCSC terms: https://genome.ucsc.edu/license/"),
				)
				dialog.NewCustomConfirm("Welcome to VCF Lift", "Set up now", "Later", content, func(ok bool) {
					if !ok {
						return
					}
					tabs.SelectIndex(1)
					startReferencePreparation()
				}, w).Show()
			})
		}()
	}
	a.Run()
}

func buildHeader() fyne.CanvasObject {
	bar := canvas.NewRectangle(palette.Accent)
	bar.SetMinSize(fyne.NewSize(4, 42))
	brand := canvasLabel("VCF Lift", 21, true, palette.Ink)
	subtitle := canvasLabel("Genome build conversion", 12, false, palette.Muted)

	badge := pill(container.NewHBox(
		canvasLabel("hg38", 13, true, palette.AccentStrong),
		canvasLabel("to", 12, false, palette.Muted),
		canvasLabel("hg19", 13, true, palette.AccentStrong),
	), palette.AccentSoft)

	left := container.NewHBox(bar, container.NewVBox(brand, subtitle))
	content := container.New(layout.NewCustomPaddedLayout(12, 12, 18, 18),
		container.NewBorder(nil, nil, left, badge, layout.NewSpacer()))
	return container.NewVBox(container.NewStack(canvas.NewRectangle(palette.Canvas), content), hairline())
}

func trustStrip() fyne.CanvasObject {
	dot := canvas.NewCircle(palette.Success)
	dotBox := container.NewGridWrap(fyne.NewSize(8, 8), dot)
	text := widget.NewLabelWithStyle("Local processing · files never leave this computer", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	text.Importance = widget.SuccessImportance
	return surfaceSoft(container.NewHBox(dotBox, text), palette.SuccessSoft, 8)
}

func sectionIntro(title, subtitle string) fyne.CanvasObject {
	t := canvasLabel(title, 20, true, palette.Ink)
	s := canvasLabel(subtitle, 13, false, palette.Muted)
	return container.NewVBox(t, s)
}

func section(step, title, subtitle string, body fyne.CanvasObject) fyne.CanvasObject {
	titleLabel := canvasLabel(title, 14, true, palette.Ink)
	subtitleLabel := canvasLabel(subtitle, 12, false, palette.Muted)
	var head fyne.CanvasObject
	switch {
	case step != "" && subtitle != "":
		head = container.NewVBox(container.NewHBox(canvasLabel(step, 14, true, palette.Accent), hspacer(6), titleLabel), subtitleLabel)
	case step != "":
		head = container.NewHBox(canvasLabel(step, 14, true, palette.Accent), hspacer(6), titleLabel)
	case subtitle != "":
		head = container.NewVBox(titleLabel, subtitleLabel)
	default:
		head = container.NewVBox(titleLabel)
	}
	return surface(container.NewVBox(head, spacer(10), body))
}

func aboutRow(label, value string) fyne.CanvasObject {
	return container.NewVBox(canvasLabel(label, 13, true, palette.Ink), mutedLabel(value))
}

func stat(label string, value *widget.Label) fyne.CanvasObject {
	return container.NewVBox(canvasLabel(strings.ToUpper(label), 10, true, palette.Muted), value)
}

func surface(content fyne.CanvasObject) fyne.CanvasObject {
	return surfaceWithColor(content, palette.Surface)
}

func surfaceWithColor(content fyne.CanvasObject, c color.Color) fyne.CanvasObject {
	bg := canvas.NewRectangle(c)
	bg.CornerRadius = 8
	bg.StrokeColor = palette.Border
	bg.StrokeWidth = 1
	return container.NewStack(bg, container.New(layout.NewCustomPaddedLayout(14, 14, 16, 16), content))
}

func surfaceSoft(content fyne.CanvasObject, c color.Color, radius float32) fyne.CanvasObject {
	bg := canvas.NewRectangle(c)
	bg.CornerRadius = radius
	return container.NewStack(bg, container.New(layout.NewCustomPaddedLayout(9, 9, 14, 14), content))
}

func pill(content fyne.CanvasObject, bg color.Color) fyne.CanvasObject {
	r := canvas.NewRectangle(bg)
	r.CornerRadius = 14
	return container.NewStack(r, container.New(layout.NewCustomPaddedLayout(6, 6, 14, 14), content))
}

func hairline() fyne.CanvasObject {
	line := canvas.NewRectangle(palette.Border)
	line.SetMinSize(fyne.NewSize(0, 1))
	return line
}

func spacer(h float32) fyne.CanvasObject {
	r := canvas.NewRectangle(color.Transparent)
	r.SetMinSize(fyne.NewSize(0, h))
	return r
}

func hspacer(w float32) fyne.CanvasObject {
	r := canvas.NewRectangle(color.Transparent)
	r.SetMinSize(fyne.NewSize(w, 1))
	return r
}

func canvasLabel(text string, size float32, bold bool, c color.Color) *canvas.Text {
	t := canvas.NewText(text, c)
	t.TextSize = size
	if bold {
		t.TextStyle = fyne.TextStyle{Bold: true}
	}
	return t
}

func mutedLabel(text string) *widget.Label {
	l := widget.NewLabel(text)
	l.Importance = widget.LowImportance
	l.Wrapping = fyne.TextWrapWord
	return l
}

func valueLabel(text string) *widget.Label {
	return widget.NewLabelWithStyle(text, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
}

func conversionPhaseTitle(stage model.Stage) string {
	switch conversionPhase(stage) {
	case 0:
		return "Preparing input"
	case 1:
		return "Converting variants"
	case 2:
		return "Creating index"
	case 3:
		return "Running quality checks"
	case 4:
		return "Conversion complete"
	default:
		return "Working"
	}
}

func conversionPhaseBody(stage model.Stage) string {
	switch conversionPhase(stage) {
	case 0:
		return "Inspecting the input and preparing any required references or runtime components."
	case 1:
		return "Validating hg38 alleles, running allele-aware liftover, validating hg19 and writing the compressed output."
	case 2:
		return "Creating the tabix index for fast region access."
	case 3:
		return "Checking candidate conservation, placeholders, output counts and rejected variants."
	case 4:
		return "The hg19 VCF, index and QC report are ready."
	default:
		return "VCF Lift is working on the conversion."
	}
}

func conversionPhase(stage model.Stage) int {
	switch stage {
	case model.StageInspecting, model.StagePreparing, model.StageResources, model.StageRuntime, model.StageGVCFValidation, model.StageGenotyping:
		return 0
	case model.StageSourceValidation, model.StageLiftover, model.StageTargetValidation, model.StageSorting:
		return 1
	case model.StageIndexing:
		return 2
	case model.StageQC:
		return 3
	case model.StageComplete:
		return 4
	default:
		return 0
	}
}

type workflowRow struct {
	status *canvas.Text
}

type workflowTracker struct {
	rows   []*workflowRow
	active int
}

func newWorkflowTracker() (*workflowTracker, fyne.CanvasObject) {
	defs := []struct{ title, detail string }{
		{"Prepare input", "Inspect file and prepare required local components"},
		{"Convert variants", "Validate hg38, lift alleles, validate hg19 and write output"},
		{"Create index", "Build the output TBI index"},
		{"Quality checks", "Verify counts, placeholders and rejected variants"},
	}
	t := &workflowTracker{active: -1}
	box := container.NewVBox()
	for i, d := range defs {
		status := canvasLabel("WAITING", 11, true, palette.Muted)
		title := widget.NewLabelWithStyle(d.title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
		detail := mutedLabel(d.detail)
		row := container.NewBorder(nil, nil, nil, status, container.NewVBox(title, detail))
		box.Add(row)
		if i < len(defs)-1 {
			box.Add(spacer(6))
		}
		t.rows = append(t.rows, &workflowRow{status: status})
	}
	return t, box
}

func (t *workflowTracker) Reset() {
	t.active = -1
	for _, r := range t.rows {
		setCanvasStatus(r.status, "WAITING", palette.Muted)
	}
}

func (t *workflowTracker) Start() {
	if len(t.rows) == 0 {
		return
	}
	t.active = 0
	setCanvasStatus(t.rows[0].status, "RUNNING", palette.Accent)
}

func (t *workflowTracker) Update(e model.ProgressEvent) {
	if e.Stage == model.StageComplete {
		t.Complete()
		return
	}
	idx := conversionPhase(e.Stage)
	if idx >= len(t.rows) {
		idx = len(t.rows) - 1
	}
	if idx < 0 {
		return
	}
	for i := 0; i < len(t.rows); i++ {
		switch {
		case i < idx:
			setCanvasStatus(t.rows[i].status, "DONE", palette.Success)
		case i == idx:
			setCanvasStatus(t.rows[i].status, "RUNNING", palette.Accent)
		case i > idx && i > t.active:
			setCanvasStatus(t.rows[i].status, "WAITING", palette.Muted)
		}
	}
	t.active = idx
}

func (t *workflowTracker) Complete() {
	for _, r := range t.rows {
		setCanvasStatus(r.status, "DONE", palette.Success)
	}
	t.active = len(t.rows) - 1
}

func (t *workflowTracker) Fail() {
	if t.active >= 0 && t.active < len(t.rows) {
		setCanvasStatus(t.rows[t.active].status, "STOPPED", palette.Danger)
	}
}

type resourceRow struct {
	ID           string
	View         fyne.CanvasObject
	Download     *canvas.Text
	Prepare      *canvas.Text
	Progress     *widget.ProgressBar
	NeedsPrepare bool
}

func makeResourceRows(manifest resources.Manifest) map[string]*resourceRow {
	friendly := map[string]struct{ title, detail string }{
		"hg38_fasta":         {"hg38 reference", "Source reference genome"},
		"hg19_fasta":         {"hg19 reference", "Target reference genome"},
		"hg38_to_hg19_chain": {"hg38 to hg19 chain", "UCSC coordinate mapping"},
		"hg38_aliases":       {"Chromosome aliases", "Contig naming compatibility"},
	}
	out := map[string]*resourceRow{}
	for _, r := range manifest.Resources {
		label := friendly[r.ID]
		if label.title == "" {
			label.title = r.Name
			label.detail = "Required conversion resource"
		}
		download := canvasLabel("NOT READY", 11, true, palette.Muted)
		download.Alignment = fyne.TextAlignTrailing
		prepare := canvasLabel("WAITING", 11, true, palette.Muted)
		prepare.Alignment = fyne.TextAlignTrailing
		progress := widget.NewProgressBar()
		progress.Hide()
		title := widget.NewLabelWithStyle(label.title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
		detail := mutedLabel(label.detail)
		detail.Wrapping = fyne.TextWrapOff
		body := container.NewVBox(
			container.NewBorder(nil, nil, container.NewVBox(title, detail), container.NewVBox(download, prepare)),
			progress,
		)
		out[r.ID] = &resourceRow{ID: r.ID, View: body, Download: download, Prepare: prepare, Progress: progress, NeedsPrepare: r.Transform == resources.TransformGzipFASTA}
	}
	return out
}

func (r *resourceRow) SetReady(ready bool) {
	if ready {
		setCanvasStatus(r.Download, "VERIFIED", palette.Success)
		if r.NeedsPrepare {
			setCanvasStatus(r.Prepare, "READY", palette.Success)
		} else {
			setCanvasStatus(r.Prepare, "NOT REQUIRED", palette.Faint)
		}
		r.Progress.Hide()
		return
	}
	setCanvasStatus(r.Download, "NOT READY", palette.Muted)
	if r.NeedsPrepare {
		setCanvasStatus(r.Prepare, "WAITING", palette.Muted)
	} else {
		setCanvasStatus(r.Prepare, "NOT REQUIRED", palette.Faint)
	}
	r.Progress.Hide()
}

func (r *resourceRow) Update(e model.ProgressEvent) {
	msg := strings.ToLower(e.Message)
	switch {
	case strings.HasPrefix(msg, "downloading"):
		setCanvasStatus(r.Download, "DOWNLOADING", palette.Accent)
		if r.NeedsPrepare {
			setCanvasStatus(r.Prepare, "WAITING", palette.Muted)
		}
		if e.Total > 0 {
			value := float64(e.Current) / float64(e.Total)
			if value < 0 {
				value = 0
			}
			if value > 1 {
				value = 1
			}
			r.Progress.SetValue(value)
			r.Progress.Show()
		}
	case strings.HasPrefix(msg, "preparing"):
		setCanvasStatus(r.Download, "VERIFIED", palette.Success)
		setCanvasStatus(r.Prepare, "PREPARING", palette.Accent)
		r.Progress.Hide()
	}
}

func setCanvasStatus(t *canvas.Text, text string, c color.Color) {
	t.Text = text
	t.Color = c
	canvas.Refresh(t)
}

func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func displayKind(kind model.FileKind) string {
	switch kind {
	case model.FileKindGVCF:
		return "gVCF"
	case model.FileKindVCF:
		return "VCF"
	default:
		return strings.ToUpper(string(kind))
	}
}
