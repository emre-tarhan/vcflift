# CERTIFICATE.md — Reference dictionary compatibility verdict (DRAFT, v1.2 pre-work)

> **Status: rev 3 (expert-approved) — implemented in v1.2.0-dev:
> `internal/certificate` (fai dictionary comparison, alias normalization,
> exact verdict strings), converter wiring (record-level `CheckREF` skipped
> on incompatible; conversion completes; outputs kept), CLI exit code 3 with
> the fixed state sentence.**
> This document defines what VCF Lift says about a *user-provided reference
> FASTA* compared against a naming profile's expected contig dictionary. It is
> a comparison verdict — nothing more.

## 1. What this is, and the words it may not use

When a user supplies their own reference FASTA (today: `--grch37-fasta` for the
second REF check with `grch37-primary`/`hs37d5`), VCF Lift compares that
FASTA's contig dictionary against the profile's expected dictionary and states
one of exactly two verdicts:

- **Compatible with this dictionary**
- **Incompatible with this dictionary** (+ first reason)

Binding language rules for every user-facing surface (CLI, GUI, report):

1. The words **"legal"**, **"valid"**, and **"invalid"** are never used. The
   verdict is about agreement with one named dictionary, not about the file's
   legitimacy. A FASTA can be a perfect, official reference and still be
   incompatible with the `grch37-primary` dictionary.
2. The verdict never grades severity beyond the two values. No "mostly
   compatible", no percentages.
3. The verdict is a claim about contig names, lengths, and presence classes
   only — the criteria in Section 4 — and says so.

## 2. Non-goals (promises, not omissions)

- **No repair.** An incompatible FASTA is reported, never rewritten, trimmed,
  renamed, or "fixed". Every incompatible verdict carries the sentence
  *"The FASTA was not modified."*
- **No download.** A mismatch never triggers fetching a replacement reference.
  Managed cache resources are untouched by user-FASTA problems.
- **No PAR claim.** PAR masking is not measured. It is excluded from the
  verdict *in writing*: every report that carries a verdict also carries
  `"PAR masking is not measured and is not part of this verdict."` An
  unmeasured criterion is never silently skipped and never counted.

## 3. What gets certified, and what never does

| Object | Certified? |
| --- | --- |
| User-provided FASTA (`--grch37-fasta`, and any future user-FASTA flag) | yes, on every use |
| Managed cache FASTAs (engine resources) | no — they are verified by the existing manifest/score mechanism; self-certification would be theater |
| Output VCF header dictionary | no — the output dictionary is built by the profile itself; it is *defined*, not *certified* |

The certificate runs **before** the record-level `profile.CheckREF` pass. If
the dictionary verdict is incompatible, the record-level check against that
FASTA is skipped (see Section 7) — a per-record REF check against a dictionary
that already fails is noise, not evidence.

## 4. Criteria (expert-fixed list)

Compared, after alias normalization of names (Section 5):

1. **Contig set** — every expected primary contig present, and nothing beyond
   the expected set.
2. **Lengths** — each contig's `fasta.fai` length equals the dictionary length.
3. **MT: 16569 or 16571** — the profile dictionaries carry the rCRS MT
   (`16569`). A `16571` MT is the old `NC_001807` sequence (hg19 `chrM`) and
   is the primary discriminator between an hg19 FASTA and a GRCh37 one:
   1–22/X/Y lengths are sequence-identical between hg19 and GRCh37, so MT is
   where they part ways.
4. **Decoy contigs present?** — extras matching the decoy name patterns
   (pattern table fixed at implementation time from the official hs37d5/GRCh38
   reference releases in cache; reported as a count plus first examples).
5. **EBV contig present?** — `chrEBV` / `NC_007605.1`.

PAR masking: not measured, not a criterion (Section 2).

Expected dictionaries have a single source of truth: the profile package
(`grch37PrimaryContigs` today; the grch38 primary table that arrives with
`grch38-primary`). The certificate **imports** that table; it never re-lists
contigs, so it cannot drift from the header writer. Both GRCh37 and hg38
primary dictionaries are 25 contigs, MT = rCRS `16569`.

## 5. Comparison algorithm

1. Require `<fasta>.fai` (as `profile.CheckREF` already does). Missing index →
   existing error path, no verdict attempted.
2. Read contig names + lengths from the `.fai` only (dictionary-level pass; no
   sequence is read).
3. Normalize names via the managed chromAlias resources (offline, already in
   cache since the reverse-direction work): every alias that maps to an
   expected contig counts as that contig. Names that map nowhere stay
   as-is and land in the extras bucket.
4. Compute: missing expected contigs; per-contig length mismatches (MT
   special-cased wording); extras partitioned into decoy / EBV / other.
5. Verdict + first reason, in this order (first hit wins):

| # | Condition | First reason (exact string) |
| --- | --- | --- |
| 1 | expected contig absent | `contig <name> is missing from the FASTA` |
| 2 | MT length ≠ 16569 | `MT length <n> is the old NC_001807 sequence (hg19 chrM); this dictionary carries the rCRS MT (16569)` |
| 3 | other contig length mismatch | `contig <name> length <n> does not match the expected <m>` |
| 4 | decoy-pattern extras present | `<k> decoy contigs present (e.g. <first>); the <profile> profile dictionary is primary-only and does not include decoys` |
| 5 | EBV contig present | `EBV contig present (<name>); the <profile> dictionary does not include EBV` |
| 6 | other extras present | `<k> unexpected contigs present (e.g. <first>)` |

Reason order is fixed so the same FASTA always yields the same sentence.
Reason wording states the dictionary's scope, never the file's quality: an
official hs37d5 FASTA is *expected* to be incompatible under these profiles,
and rows 4–5 say why in terms of what the dictionary carries — the user's
file is not called broken.
The expert example resolves exactly as required: an hg19 FASTA under
`grch37-primary` — names alias cleanly, 1–22/X/Y lengths match, MT is
`16571` — yields **Incompatible**, first reason = row 2, and the file is not
touched.

## 6. Verdict strings (exact)

Compatible:

```
Compatible with the <profile> dictionary: 25 primary contigs, all lengths
match, MT is the rCRS (16569). PAR masking is not measured and is not part
of this verdict.
```

Incompatible:

```
Incompatible with the <profile> dictionary: <first reason>. The FASTA was
not modified. PAR masking is not measured and is not part of this verdict.
```

`<profile>` is the frozen profile identifier (`grch37-primary`, `hs37d5`,
future `grch38-primary`).

## 7. Behavior linkage (expert-fixed, replaces fail-hard)

The lift itself was already validated against the managed references (source
REF check, target REF check — the standing runtime invariants). The user FASTA
is a **second contract**, layered on top; its failure must not undo work the
first contract already validated.

- **Compatible** → record-level `CheckREF` runs as today.
- **Incompatible** → record-level `CheckREF` is skipped; the conversion itself
  completes: output VCF, indexes, ledger sidecar, and the QC report (carrying
  the verdict) are all written and kept. The dictionary check ran and
  returned a verdict; only the record-level pass was skipped. The fixed
  sentence for this state: "dictionary check completed: incompatible;
  record-level REF check skipped." The process exits **non-zero because the
  verdict is incompatible** — not because a check was skipped — and the
  verdict stands in the report. The exit code stays distinct from engine
  failure codes.

Two prohibitions, verbatim:

- looking like success is forbidden, and looking like no check ran is
  equally forbidden — the dictionary check did run and returned
  incompatible; the fixed sentence plus the non-zero exit record exactly
  that;
- deleting or undoing already-written outputs because of the check is
  forbidden.

This replaces the earlier fail-hard draft (expert review, 2026-10-04): a
dictionary verdict must not discard a conversion that the managed-reference
contract already validated. It also replaces the current raw first-error path
(`grch37 FASTA has no contig …`, record-N REF mismatch) with the
dictionary-level verdict naming the assembly-level cause first.

## 8. Report and display

### Report JSON (only when a user FASTA was provided; `omitempty` otherwise)

```json
"fasta_certificate": {
  "path": "/path/to/user.fa",
  "verdict": "incompatible",
  "first_reason": "MT length 16571 is the old NC_001807 sequence (hg19 chrM); this dictionary carries the rCRS MT (16569)",
  "expected_contigs": 25,
  "matched_contigs": 25,
  "missing_contigs": [],
  "length_mismatches": [{"contig": "MT", "length": 16571, "expected": 16569}],
  "extra_contigs": {"decoy": 0, "ebv": false, "other": 0, "examples": []},
  "par_measured": false
}
```

`par_measured` is a constant `false` until PAR is actually measured — the
field exists so the exclusion is machine-readable, not just prose.

### CLI

Verdict printed once at the end of the run using the exact strings of
Section 6, followed on incompatibility by the fixed state sentence:
"dictionary check completed: incompatible; record-level REF check skipped."
Incompatible → documented non-zero exit code, distinct from engine failures;
outputs and report untouched (Section 7).

### GUI

The FASTA option is CLI-only today, so there is no GUI surface to change yet.
When a GUI reference-file input ever appears, the display rule is: **one plain
sentence in the completion dialog**, same words as Section 6, with the
completion state carrying the fixed sentence of Section 7 (never a plain
success, never "check not run"), e.g.:

> Your reference file does not match the GRCh37 primary dictionary: MT is
> 16571 (the older sequence, hg19 chrM); expected 16569 (rCRS). Your file was
> not changed.

No tables, no per-contig listings, no severity grading in the GUI — the JSON
report carries the detail.

## 9. Scope for grch38-primary (v1.2 sibling work)

The certificate machinery is dictionary-generic. When `grch38-primary` lands,
its gate is name equality **plus** post-left-align benchmark overlap against
VCF Lift's own managed hg38 FASTA (REF + dictionary; internal verification,
unchanged) — same-namespace equality never replaces the cross-check. The
user-FASTA certificate applies the same Sections 4–8 with the hg38 primary
dictionary and the hg38 alias set. No new verdict states, no new words.

## 10. Decisions — resolved by expert review (2026-10-04)

1. **Strict extras rule stands**: any decoy/EBV/other extra contig →
   incompatible. An official hs37d5 FASTA is incompatible under these
   profiles by design; the fixed reason sentences (Section 5) explain the
   dictionary's scope instead of calling the file broken. The compatible
   sentence keeps saying "25 primary contigs".
2. **No fail-hard.** Incompatible → skip record-level `CheckREF`, write all
   outputs, exit non-zero **because the verdict is incompatible** (Section 7);
   fixed sentence: "dictionary check completed: incompatible; record-level
   REF check skipped."
3. **Reason order frozen** as listed in Section 5: missing → MT length →
   other lengths → decoys → EBV → other extras.
