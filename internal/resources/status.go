package resources

import "path/filepath"

// ResourceStatus is a lightweight local-cache view used by the desktop UI and
// diagnostics. It does not re-download or checksum remote data;
// resources were checksum-verified before activation and readiness is based on
// the prepared files required by the conversion pipeline.
type ResourceStatus struct {
	ID        string
	Name      string
	Ready     bool
	Path      string
	Transform Transform `json:"-"`
}

// Status returns the current local readiness of every resource in manifest.
func (m *Manager) Status(manifest Manifest) []ResourceStatus {
	out := make([]ResourceStatus, 0, len(manifest.Resources))
	for _, r := range manifest.Resources {
		path := filepath.Join(m.Root, r.Filename)
		if r.Transform == TransformGzipFASTA && r.PreparedFilename != "" {
			path = filepath.Join(m.Root, r.PreparedFilename)
		}
		out = append(out, ResourceStatus{
			ID:        r.ID,
			Name:      r.Name,
			Ready:     m.resourceReady(r),
			Path:      path,
			Transform: r.Transform,
		})
	}
	return out
}

// AllReady reports whether every manifest resource required by the reference
// profile is already prepared locally.
func (m *Manager) AllReady(manifest Manifest) bool {
	for _, r := range manifest.Resources {
		if !m.resourceReady(r) {
			return false
		}
	}
	return true
}
