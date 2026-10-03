package converter

import (
	"context"
	"errors"
	"fmt"

	"github.com/emre-tarhan/vcflift/internal/engine"
	"github.com/emre-tarhan/vcflift/internal/enginebundle"
)

type EngineState struct {
	Source       string                 `json:"source"`
	Installation engine.Installation    `json:"installation"`
	Manifest     *enginebundle.Manifest `json:"manifest,omitempty"`
}

// ResolveEngine implements the reproducible engine priority used by both GUI
// and conversion: explicit override, embedded pinned bundle, installed local
// bundle, then a system PATH fallback for development/advanced use.
func (c *NativeConverter) ResolveEngine(ctx context.Context) (EngineState, error) {
	if c.Installation.BCFTools != "" {
		inst, err := validatePinnedEngine(ctx, c.Installation)
		if err != nil {
			return EngineState{}, err
		}
		c.Installation = inst
		return EngineState{Source: "explicit", Installation: inst}, nil
	}

	if c.EngineBundle != nil {
		if inst, manifest, err := c.EngineBundle.Install(); err == nil {
			inst, err = validatePinnedEngine(ctx, inst)
			if err != nil {
				return EngineState{}, fmt.Errorf("embedded engine failed preflight: %w", err)
			}
			c.Installation = inst
			return EngineState{Source: "embedded", Installation: inst, Manifest: &manifest}, nil
		} else if !errors.Is(err, enginebundle.ErrBundleUnavailable) {
			return EngineState{}, fmt.Errorf("prepare embedded native engine: %w", err)
		}

		if inst, manifest, err := c.EngineBundle.FindInstalled(); err == nil {
			inst, err = validatePinnedEngine(ctx, inst)
			if err != nil {
				return EngineState{}, fmt.Errorf("installed engine failed preflight: %w", err)
			}
			c.Installation = inst
			return EngineState{Source: "installed_bundle", Installation: inst, Manifest: &manifest}, nil
		} else if !errors.Is(err, enginebundle.ErrBundleUnavailable) {
			return EngineState{}, fmt.Errorf("load installed native engine: %w", err)
		}
	}

	if pathInst := engine.ResolvePathInstallation(); pathInst.BCFTools != "" {
		inst, err := validatePinnedEngine(ctx, pathInst)
		if err != nil {
			return EngineState{}, fmt.Errorf("system bcftools is not compatible: %w", err)
		}
		c.Installation = inst
		return EngineState{Source: "system_path", Installation: inst}, nil
	}
	return EngineState{}, fmt.Errorf("%w; this build contains no native engine payload for %s and no verified local bundle is installed", engine.ErrEngineNotFound, enginebundle.PlatformKey())
}

func validatePinnedEngine(ctx context.Context, inst engine.Installation) (engine.Installation, error) {
	validated, err := engine.ValidateInstallation(ctx, inst)
	if err != nil {
		return validated, err
	}
	if err := engine.RequireVersion(validated, engine.RequiredBCFToolsVersion); err != nil {
		return validated, err
	}
	return validated, nil
}
