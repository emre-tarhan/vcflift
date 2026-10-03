# Optional gVCF runtime

VCF Lift downloads the following only when the recommended gVCF workflow is used. They are not part of VCF Lift's MIT-licensed source code.

## GATK

- Project: Broad Institute Genome Analysis Toolkit
- Pin: 4.7.0.0
- Source/release: https://github.com/broadinstitute/gatk
- License: Apache License 2.0 (subject to notices inside the upstream distribution)

## Eclipse Temurin

- Project: Eclipse Temurin / Adoptium
- Pin: JRE 17.0.20.1+1
- Source/releases: https://github.com/adoptium/temurin17-binaries
- The binary distribution carries its own OpenJDK/Temurin legal and notice files. VCF Lift downloads the upstream archive rather than repackaging it.

## Integrity

Temurin archives are checked against upstream SHA-256 sidecars. GATK is resolved by exact release tag and checked against the SHA-256 `digest` reported by the GitHub Releases API. No runtime archive is executed before successful integrity verification. When both managed components are missing, their downloads may run concurrently; verification and extraction remain independent and deterministic.

# Native VCF engine

Official release builds embed a native engine built from BCFtools 1.24 plus the `freeseek/score` liftover plugin. The source build scripts copy the upstream license texts into the engine payload so they are covered by the same SHA-256 manifest as the executable/plugin files.

Locally imported engine bundles may come from user-supplied binaries. Their manifest records exact file hashes and the supplied plugin source reference (or `local-unverified` when unknown); VCF Lift does not claim provenance that it cannot verify.
