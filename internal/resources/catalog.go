package resources

// DefaultManifest is the exact UCSC hg38 -> hg19 profile used by VCF Lift.
// The chain is downloaded at runtime and is never redistributed inside our
// binaries. Its checksum is resolved from UCSC's authoritative md5sum index.
func DefaultManifest() Manifest {
	return Manifest{
		Version: 1,
		Resources: []Resource{
			{
				ID:               "hg38_fasta",
				Name:             "UCSC hg38 reference FASTA",
				URL:              "https://hgdownload.soe.ucsc.edu/goldenPath/hg38/bigZips/hg38.fa.gz",
				Filename:         "hg38.fa.gz",
				MD5:              "1c9dcaddfa41027f17cd8f7a82c7293b",
				Transform:        TransformGzipFASTA,
				PreparedFilename: "hg38.fa",
				DownloadBytes:    938 << 20,
				PreparedBytes:    3300 << 20,
			},
			{
				ID:               "hg19_fasta",
				Name:             "UCSC hg19 reference FASTA",
				URL:              "https://hgdownload.soe.ucsc.edu/goldenPath/hg19/bigZips/latest/hg19.fa.gz",
				Filename:         "hg19.fa.gz",
				MD5:              "7707462fc100c7d987c075bc146b16ae",
				Transform:        TransformGzipFASTA,
				PreparedFilename: "hg19.fa",
				DownloadBytes:    934 << 20,
				PreparedBytes:    3200 << 20,
			},
			{
				ID:                 "hg38_to_hg19_chain",
				Name:               "UCSC hg38 to hg19 liftOver chain",
				URL:                "https://hgdownload.soe.ucsc.edu/goldenPath/hg38/liftOver/hg38ToHg19.over.chain.gz",
				Filename:           "hg38ToHg19.over.chain.gz",
				ChecksumIndexURL:   "https://hgdownload.soe.ucsc.edu/goldenPath/hg38/liftOver/md5sum.txt",
				ChecksumIndexName:  "hg38ToHg19.over.chain.gz",
				Transform:          TransformNone,
				LicenseURL:         "https://genome.ucsc.edu/license/",
				RequiresAcceptance: true,
				DownloadBytes:      2 << 20,
			},
			{
				ID:            "hg38_aliases",
				Name:          "UCSC hg38 chromosome aliases",
				URL:           "https://hgdownload.soe.ucsc.edu/goldenPath/hg38/bigZips/hg38.chromAlias.txt",
				Filename:      "hg38.chromAlias.txt",
				MD5:           "889533482362d1c7d2976e12d8367737",
				Transform:     TransformNone,
				DownloadBytes: 1 << 20,
			},
		},
	}
}
