package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// VCF Lift uses one light visual system: a broken-white canvas, white
// cards with hairline borders, a readable ink/slate text ramp and a single
// restrained steel-blue accent. The disabled tone is kept
// readable: Fyne renders secondary (LowImportance) labels with the theme's
// disabled color, so it doubles as the secondary text tone.
var palette = struct {
	Canvas, Surface, SurfaceAlt, Border        color.NRGBA
	Ink, Text, Muted, Faint                    color.NRGBA
	Accent, AccentStrong, AccentSoft, Focus    color.NRGBA
	Success, SuccessSoft, Warning, Danger      color.NRGBA
	Disabled, DisabledButton, InputBg, PH      color.NRGBA
}{
	Canvas:          color.NRGBA{R: 241, G: 243, B: 246, A: 255},  // canvas
	Surface:         color.NRGBA{R: 255, G: 255, B: 255, A: 255},  // card
	SurfaceAlt:      color.NRGBA{R: 237, G: 241, B: 244, A: 255},  // inset strip
	Border:          color.NRGBA{R: 220, G: 226, B: 232, A: 255},  // hairline
	Ink:             color.NRGBA{R: 24, G: 36, B: 48, A: 255},     // titles
	Text:            color.NRGBA{R: 46, G: 60, B: 73, A: 255},     // body
	Muted:           color.NRGBA{R: 92, G: 108, B: 123, A: 255},   // secondary text
	Faint:           color.NRGBA{R: 147, G: 161, B: 173, A: 255},  // caps-only labels
	Accent:          color.NRGBA{R: 53, G: 97, B: 143, A: 255},    // steel blue
	AccentStrong:    color.NRGBA{R: 43, G: 80, B: 122, A: 255},    // accent text on soft surfaces
	AccentSoft:      color.NRGBA{R: 228, G: 237, B: 246, A: 255},  // soft accent wash
	Focus:           color.NRGBA{R: 127, G: 163, B: 198, A: 255},  // focus ring
	Success:         color.NRGBA{R: 62, G: 125, B: 94, A: 255},
	SuccessSoft:     color.NRGBA{R: 227, G: 240, B: 232, A: 255},
	Warning:         color.NRGBA{R: 169, G: 122, B: 44, A: 255},
	Danger:          color.NRGBA{R: 176, G: 80, B: 73, A: 255},
	Disabled:        color.NRGBA{R: 107, G: 123, B: 137, A: 255},  // readable secondary-label tone
	DisabledButton:  color.NRGBA{R: 231, G: 235, B: 239, A: 255},
	InputBg:         color.NRGBA{R: 248, G: 250, B: 251, A: 255},
	PH:              color.NRGBA{R: 132, G: 150, B: 164, A: 255},  // placeholder
}

type genomeTheme struct{ base fyne.Theme }

func newGenomeTheme() fyne.Theme { return &genomeTheme{base: theme.DefaultTheme()} }

func (t *genomeTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	switch name {
	case theme.ColorNameBackground:
		return palette.Canvas
	case theme.ColorNameForeground:
		return palette.Text
	case theme.ColorNamePrimary, theme.ColorNameHyperlink:
		return palette.Accent
	case theme.ColorNameForegroundOnPrimary:
		return color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	case theme.ColorNameFocus:
		return palette.Focus
	case theme.ColorNameSelection:
		return color.NRGBA{R: palette.Accent.R, G: palette.Accent.G, B: palette.Accent.B, A: 48}
	case theme.ColorNameSeparator:
		return palette.Border
	case theme.ColorNamePlaceHolder:
		return palette.PH
	case theme.ColorNameInputBackground:
		return palette.InputBg
	case theme.ColorNameInputBorder:
		return palette.Border
	case theme.ColorNameButton:
		// Medium-importance buttons sit on white cards; a soft gray chip keeps
		// them visible where the default white would disappear.
		return color.NRGBA{R: 239, G: 242, B: 245, A: 255}
	case theme.ColorNameDisabled:
		return palette.Disabled
	case theme.ColorNameDisabledButton:
		return palette.DisabledButton
	case theme.ColorNameHover:
		// Fyne alpha-blends Hover/Pressed over the button background, so these
		// must stay translucent: an opaque value would repaint primary buttons
		// near-white and hide their foreground text.
		return color.NRGBA{R: 24, G: 36, B: 48, A: 16}
	case theme.ColorNamePressed:
		return color.NRGBA{R: 24, G: 36, B: 48, A: 30}
	case theme.ColorNameMenuBackground, theme.ColorNameOverlayBackground, theme.ColorNameHeaderBackground:
		return palette.Surface
	case theme.ColorNameSuccess:
		return palette.Success
	case theme.ColorNameWarning:
		return palette.Warning
	case theme.ColorNameError:
		return palette.Danger
	}
	return t.base.Color(name, theme.VariantLight)
}

func (t *genomeTheme) Font(style fyne.TextStyle) fyne.Resource    { return t.base.Font(style) }
func (t *genomeTheme) Icon(name fyne.ThemeIconName) fyne.Resource { return t.base.Icon(name) }
func (t *genomeTheme) Size(name fyne.ThemeSizeName) float32       { return t.base.Size(name) }
