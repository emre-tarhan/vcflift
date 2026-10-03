package resources

// Transform describes how a downloaded resource is prepared for use.
type Transform string

const (
	TransformNone      Transform = "none"
	TransformGzipFASTA Transform = "gzip_fasta"
)

// Resource describes one externally managed artifact. Resources are downloaded
// at runtime rather than bundled so large references stay out of release assets
// and third-party distribution terms can be respected.
type Resource struct {
	ID                 string    `json:"id"`
	Name               string    `json:"name"`
	URL                string    `json:"url"`
	Filename           string    `json:"filename"`
	MD5                string    `json:"md5,omitempty"`
	ChecksumIndexURL   string    `json:"checksum_index_url,omitempty"`
	ChecksumIndexName  string    `json:"checksum_index_name,omitempty"`
	Transform          Transform `json:"transform"`
	PreparedFilename   string    `json:"prepared_filename,omitempty"`
	LicenseURL         string    `json:"license_url,omitempty"`
	RequiresAcceptance bool      `json:"requires_acceptance,omitempty"`
	DownloadBytes      int64     `json:"download_bytes,omitempty"`
	PreparedBytes      int64     `json:"prepared_bytes,omitempty"`
}

type Manifest struct {
	Version   int        `json:"version"`
	Resources []Resource `json:"resources"`
}
