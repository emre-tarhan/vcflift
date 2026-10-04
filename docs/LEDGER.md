# LEDGER.md — Conversion ledger design (DRAFT, v1.2 pre-work)

> **Status: rev 3 (expert-approved) — implemented in v1.2.0-dev:
> `internal/ledger` (letter-tree classifier, gz TSV sidecar with verbatim
> `FLIP`/`SWAP` copy and `reason` column, report counts, GUI sentences).
> `left_align_representation_change` stays reserved with a zero counter; no
> assignment function exists.**
> This document defines the per-record conversion ledger: a classification of
> what the liftover actually did to each lifted record, a sidecar file that
> records it, and one summary surface (report + one GUI sentence).

## 1. Purpose

The QC report already counts lifted and rejected records. It cannot say *what
changed* for the records that lifted. The ledger closes that gap with an
honest, per-record classification — honest meaning: classes are defined only
over what is observable in the output record itself, and the limits of that
observability are written down (Section 5).

The ledger is an audit artifact, not a second QC gate. It never blocks, fails,
or rewrites a conversion.

## 2. Data source (already present, no engine change)

Every v1.1.1 production output carries per-record source annotations written by
the liftover plugin's `--write-src` (verified in real v1.1.1 outputs):

| INFO tag | Type | Content |
| --- | --- | --- |
| `SRC_CHROM` | String | source contig name, pre-liftover (source-side naming) |
| `SRC_POS` | Integer | source position, pre-liftover |
| `SRC_REF_ALT` | String list | source alleles, REF first then ALTs (`G,A`) |

The plugin also emits two further tags:

| INFO tag | Type | Content |
| --- | --- | --- |
| `FLIP` | Flag | plugin reports alleles flipped strand during liftover |
| `SWAP` | Integer | which ALT became the target REF (`-1` = new reference) |

`FLIP` and `SWAP` are the plugin's own account of what it did, from the same
source as `SRC_*` (freeseek/score `--flip-tag` default `FLIP`, `--swap-tag`
default `SWAP`; `SWAP` names which ALT became the target REF, `-1` = a new
reference arrived). Trusting `SRC_*` while discarding these two as "plugin
internals" is not a consistent trust boundary: without them, a reverse-strand
`A/G` → `T/C` record lands in `unclassifiable`, the bucket fills, and the
ledger looks broken when in fact the plugin classified the event and we chose
not to read it.

Resolution (expert review, 2026-10-04): **no new class, still no strand
class.** The two tags are copied verbatim into the sidecar (`plugin_flip`,
`plugin_swap`) — copied, never derived from letters — and are not consumed by
the classification tree. The tree stays a letter tree (Section 4). The GUI
reports them as a separate sentence, not as a class (Section 7).

The classifier therefore reads exactly: `SRC_CHROM`, `SRC_POS`, `SRC_REF_ALT`,
`CHROM`, `POS`, `REF`, `ALT`. The sidecar writer additionally copies `FLIP` and
`SWAP` verbatim. No engine flags change; `--write-src` is already
unconditional in the production plan.

## 3. The classes (expert-fixed list, verbatim)

| Class | Meaning |
| --- | --- |
| `unchanged` | position and all alleles identical to the source record |
| `same_locus_allele_swap` | same position; REF and ALT roles exchanged (biallelic) |
| `position_shift` | position moved; REF/ALT byte-identical |
| `left_align_representation_change` | reserved: needs the pre-left-align target position, which no current field carries; not assigned in v1.2, counter stays zero (Section 4.1) |
| `allele_index_rewrite` | same position; allele collection rewritten (multiallelic permutation, ALT promoted to REF, or a new REF introduced) without a position move |
| `unclassifiable` | none of the above patterns matched; counted, never hidden |

Class names are frozen identifiers: they appear verbatim in the sidecar, the
report JSON, and any future CLI output. Documentation may paraphrase; code may
not rename.

There is **no strand category**. Section 5.2 explains why one cannot exist.
`plugin_flip`/`plugin_swap` are sidecar annotations, not classes; they never
enter the class column.

## 4. Classification decision tree

Run per lifted record. `S` = (src_chrom, src_pos, src_alleles[]) from `SRC_*`;
`O` = (chrom, pos, alleles[]) from the record. Alleles are compared as
uppercase strings, element-wise; `alt` split on `,`.

```
0. Integrity: any SRC_* tag missing or malformed        -> unclassifiable (missing_src)
1. Contig naming: if the profile renames contigs
   (grch37-primary / hs37d5 / future grch38-primary), map O's contig back to
   source naming via the inverse profile rename map. Still different contigs
                                                         -> unclassifiable (contig_mismatch)
   (cross-contig is deliberately NOT its own class; it surfaces through the
   reason column. Primary-to-primary chains never move a record across
   chromosomes; if it ever happens we want it visible, not buried)
2. src_pos == pos (same locus):
   a. allele lists element-wise identical                -> unchanged
   b. biallelic both sides, src_ref == alt and
      src_alt == ref (roles exchanged)                   -> same_locus_allele_swap
   c. any other role/index rewrite of the allele
      collection (multiallelic permutation; a source ALT now REF;
      a new REF letter with the old REF demoted to ALT)  -> allele_index_rewrite
   d. otherwise                                          -> unclassifiable (no_pattern)
3. src_pos != pos (moved):
   a. allele lists element-wise identical                -> position_shift
   b. anything else: `left_align_representation_change` is reserved but
      NOT assigned in v1.2 (Section 4.1)                 -> unclassifiable (no_pattern)
```

Order matters and is part of the contract: byte-identical alleles are decided
first (3a), so a record that merely moved never lands anywhere else.

With 3b unassigned in v1.2 (Section 4.1), the classifier needs no reference
reads at all: the tree runs on record letters only. No normalization code is
part of v1.2.

### 4.1 Rule 3b — `left_align_representation_change` is reserved, not assigned (expert-fixed, round 3)

A rev-2 draft proposed a "shared-prefix equivalence" (d = src_pos − pos_o,
one window read from each build, letter equality required). That rule is
wrong and is withdrawn: d conflates two different quantities — the chain
offset between builds and the left-align shift within one build. hg38
`1000001` and hg19 `1064621` can be the same locus; their difference is chain
offset, not representation prefix, and the identity `src_pos − d == pos_o` is
arithmetic, not biological — that window is a different sequence in hg38 and
hg19. Under such a rule a real left-aligned indel either lands in
`no_pattern` anyway, or — if the window equality is forced — yields a false
`left_align_representation_change`.

The class is unobservable without the pre-left-align target position.
`SRC_*` does not carry it, and no engine flag changes in this version.
Therefore, for v1.2:

- the class stays in the enum (the frozen six-class list is unchanged);
- its assignment is not written; its counter stays zero in every report;
- a left-aligned indel classifies as `unclassifiable` (`no_pattern`);
- filling the class with an offset-formula equivalence is forbidden;
- the GUI states the gap whenever the ledger line shows:
  "representation-change class is not assigned in this version."

The rule that eventually assigns this class needs the pre-left-align target
position — a future engine/source change, not a formula over existing fields.
Until then the enum slot is a promise kept by staying empty, not a bucket to
fill. The assignment function must not be written from any text in this
document.

The classifier is direction-neutral: the same tree runs for forward
(hg38→hg19) and reverse (hg19/GRCh37→hg38) conversions — it reads record
letters only, in both directions.

### Worked examples

Real v1.1.1 record (forward): `SRC_CHROM=chr1;SRC_POS=1000001;
SRC_REF_ALT=G,A` → `chr1 1064621 G A` — position moved, alleles identical →
`position_shift`.

Reverse-strand non-palindromic SNP, same mapped position: source `A/G` →
output `T/C`. Letters changed, no role or index pattern, 4.1 does not apply →
`unclassifiable` (`no_pattern`). The class column is honest: without a strand
category the letters alone cannot classify this. The sidecar row is not blind:
it carries `plugin_flip=1`, the plugin's own account, copied verbatim.

Biallelic swap at a *shifted* position (`src A/T pos p → out T/A pos p+d`):
3a fails (letters differ), 3b is unassigned → `unclassifiable`
(`no_pattern`). Swaps are only claimable at the same locus.

A genuinely left-aligned indel (position moved, allele strings re-anchored):
same path — `unclassifiable` (`no_pattern`) in v1.2, counted honestly; the
GUI gap sentence carries the explanation (Section 7).

## 5. Honest observability limits

These are binding wording rules for every surface that reports ledger data.

### 5.1 `SRC_POS`–`POS` difference alone is not a shift

An indel that was left-aligned also shows a position difference. A bare
position delta therefore proves nothing about which happened. The tree encodes
this: at a moved position, byte-identical alleles (3a) decide `position_shift`;
everything else at a moved position — including genuine left-aligned indels —
is `unclassifiable` (`no_pattern`) in v1.2 (Section 4.1). Documentation and
GUI copy must never
describe the position delta column alone as "shifted records".

### 5.2 Palindromic SNP: swap vs strand complement is unresolvable

For allele pairs A/T and C/G, complementing both letters produces exactly the
letter pattern of a REF↔ALT swap. The record letters (`SRC_*` vs `REF`/`ALT`)
are identical under the two hypotheses, so the letter tree cannot separate
them — for a palindromic pair, `same_locus_allele_swap` means "the observable
transformation was a role exchange", which is compatible with either
mechanism. Consequences, verbatim:

- the enum has **no strand category**;
- no output may state "0 strand flips" / "no strand changes occurred" — the
  correct statement is "a strand category does not exist in this ledger";
- `FLIP`/`SWAP` are copied verbatim into the sidecar and are **never derived,
  never classified** (Section 2). A row may legitimately carry both
  `same_locus_allele_swap` and `plugin_flip=1`; documentation must state that
  this coexistence is not a mechanism distinction.

### 5.3 Profile renames are not liftover classes

With `grch37-primary`/`hs37d5` (and future `grch38-primary`) the output contig
is renamed (`chr1` → `1`) while `SRC_CHROM` keeps source-side naming. The
comparator normalizes naming before classifying (tree step 1); a rename is
never counted as a record change and never appears in the sidecar as one. A
contig that still differs after normalization is surfaced as `unclassifiable`.

### 5.4 Edge cases

- Multiallelic records: allele lists compared element-wise; `*` placeholder
  alleles compare as ordinary letters.
- Sidecar covers lifted records only — liftover rejects already have their own
  sidecar (`--write-reject`), profile rejects have `.profile-rejected.vcf.gz`.
- `unclassifiable` is a first-class count, never folded into another class and
  never silently dropped; every `unclassifiable` row carries a `reason`
  (`missing_src`, `contig_mismatch`, `no_pattern`) in the sidecar.

## 6. Sidecar schema

One row per lifted record, class included. Recommendation: **plain gzip TSV**,
not VCF INFO.

Decision rationale: adding an INFO tag to the output VCF would change output
bytes and break the standing promise that `ucsc-hg19` forward conversion stays
bit-for-bit identical. A sidecar leaves the output contract untouched
(compatibility promise: outputs only gain files/fields, never mutate).

```
<output>.ledger.tsv.gz
```

Layout: gzip comment lines (tool name+version, run timestamp, input path,
class enum version), then a header row, then one row per lifted record in
output order:

| Column | Content |
| --- | --- |
| `src_chrom` | source contig (`SRC_CHROM`, source-side naming) |
| `src_pos` | source position |
| `src_ref` | source REF (first element of `SRC_REF_ALT`) |
| `src_alt` | source ALTs, comma-joined |
| `chrom` | output contig (post-profile naming, as in the VCF) |
| `pos` | output position |
| `ref` | output REF |
| `alt` | output ALTs, comma-joined |
| `class` | one of the six frozen identifiers |
| `plugin_flip` | plugin `FLIP` copied verbatim (`1`/`0`); `.` if absent — never derived |
| `plugin_swap` | plugin `SWAP` copied verbatim (integer; `-1` = new reference); `.` if absent — never derived |
| `reason` | `missing_src` / `contig_mismatch` / `no_pattern` on `unclassifiable` rows; `.` otherwise |

Written whenever at least one record lifted. No new CLI flag, no GUI control:
the ledger is always on (zero knobs; the counts are cheap and the file is an
audit artifact in the same spirit as the reject sidecar). If size ever matters
on real cohorts, an opt-out flag is a v1.2.x decision, not a v1.2 one.
In v1.2 the `class` column emits five of the six identifiers;
`left_align_representation_change` is reserved and never written
(Section 4.1).

## 7. QC summary

### Report JSON (always present when records lifted)

```json
"ledger": {
  "classified_records": 4942650,
  "unchanged": 4942600,
  "same_locus_allele_swap": 12,
  "position_shift": 30,
  "left_align_representation_change": 0,
  "allele_index_rewrite": 3,
  "unclassifiable": 5,
  "plugin_flip_records": 0,
  "plugin_swap_records": 0
}
```

Plus `ledger_sidecar` (path) when the sidecar was written. Fields only get
added, per the compatibility promise. `left_align_representation_change` is
always `0` in v1.2 — reserved, not assigned (Section 4.1); the field exists
so the schema does not change when the class one day becomes assignable.

### GUI: three sentences at most, not a dump

The completion dialog gets **at most three sentences**: the class line, the
flip sentence when the plugin set `FLIP` on any record, and the version-gap
sentence whenever the class line shows. The ledger is a record,
the dialog is not its printout ("defter değil döküm"). The class line appears
only when a non-`unchanged` count is non-zero, and is **mandatory** when
`unclassifiable` > 0:

```
Ledger: 30 position shifts · 3 allele rewrites · 2 same-locus swaps ·
1 unclassifiable
```

The representation-change count never enters this line; its explanation lives
only in the version-gap sentence. The frozen name `unclassifiable` is used
verbatim — there is no `unclassified`. When the plugin set `FLIP` on any record, a separate sentence follows:

```
Plugin reported flip on 12 records; this is not a ledger class.
```

Whenever the ledger line is shown, the version-gap sentence rides along:

```
representation-change class is not assigned in this version.
```

`plugin_swap` stays in the report JSON only; it never enters the GUI
sentences.

All-`unchanged` runs add no ledger line (the report JSON still carries the
counts). The lines never enumerate records, never use the word "shift" for a
bare position delta without the class behind it, and never state "0 strand
flips" or "no strand changes" (Section 5.2).

## 8. Implementation (v1.2.0-dev)

1. `internal/ledger` — classifier (decision tree above; letters only, no
   reference reads in v1.2), sidecar writer (gz TSV, verbatim
   `FLIP`/`SWAP` copy, `reason` column). The
   `left_align_representation_change` assignment function is deliberately
   not written (Section 4.1).
2. Hook after the profile stage in `internal/converter/native.go`, reading the
   finalized output; feed counts into `internal/report`.
3. The GUI sentences under the rules of Section 7.
4. Tests: unit fixtures per emitted class and per `unclassifiable` reason;
   verbatim copy tests for `plugin_flip`/`plugin_swap` (value passes through
   untouched, absent → `.`); a left-aligned-indel fixture asserting
   `no_pattern` and a zero `left_align_representation_change` counter; the
   honest-limit examples (palindrome row carrying both
   `same_locus_allele_swap` and `plugin_flip=1`, non-palindromic flip →
   `no_pattern` + `plugin_flip=1`, swap-at-shifted-position → `no_pattern`);
   GUI-copy assertions (gap sentence present with the ledger line,
   `plugin_swap` absent from dialog text); a real-data pass over an existing
   gate input comparing `sum(classes) == lifted_variants`.

## 9. Decisions — resolved by expert review (2026-10-04, rounds 2–3)

1. **Sidecar: always on.** No flag, no GUI control.
2. **Sidecar format: gz TSV.** VCF INFO excluded (bit-for-bit promise).
3. **`left_align_representation_change`: reserved, not assigned in v1.2**
   (round 3). Counter stays zero; left-aligned indels are `no_pattern`; an
   offset-formula equivalence must never fill the class (Section 4.1).
4. **GUI ledger line: only when a non-`unchanged` count is non-zero, and
   mandatory when `unclassifiable` > 0.** Plugin flip sentence separate; the
   version-gap sentence rides along whenever the ledger line shows
   (Section 7).
5. **`plugin_swap`: report JSON only.** Never in the GUI sentences.
