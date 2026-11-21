# W9 — Benchmark

**Goal:** make AISAI's claims falsifiable — one engine, trivial baselines, recall-weighted
metrics, and external benchmarks a third party can reproduce.

**Status:** ✅ core deliverables landed and measured — the metrics, the trivial baselines, the
TF-IDF baseline and both external adapters exist, and the static engine has been measured
against the bundled corpus **and** against the live RealVuln and OWASP BenchmarkPython
corpora. The remaining open boxes are listed in **Notes** with the reason for each.
**Depends on:** W2, W4
**Blocks:** —

## Tasks

### One engine, two harnesses
- [x] `cmd/aisai-bench/main.go` — benchmark harness
- [x] The harness calls `internal/sast` and `internal/scan` — **the same code the app calls**
- [x] **F§1.4** — a scanner config difference becomes a compile-time fact, not a discovery
- [x] Fuzz target selection, seed, and model recorded in every result row — the model column
      carries the engine and its severity policy (`aisai-sast(min-severity=low)`) and, for the
      TF-IDF baseline, the split seed and the size of the split

### Notebook compatibility
- [x] Emit a CSV with the **exact column schema** the existing notebooks read:
  - [x] `llm_*` / `bandit_*` prefixes (auto-detected by the notebooks)
  - [x] `*_classification_before` / `*_classification_after`
  - [x] `*_completed_before` / `*_completed_after` / `*_completed`
  - [x] `*_expected_label`, `expected_before`, `expected_after`
  - [x] `benchmark_input_mode` (`paired` | `single`)
  - [x] timing columns
- [ ] `bench/notebooks/llmBenchmarkCSV.ipynb` and `staticBenchmarkCSV.ipynb` run **unmodified**
      — the emitted header was checked against the column names the notebooks read, and the
      order (`llm_` before `bandit_`) satisfies both notebooks' different "first match wins"
      rules, but **the notebooks were not executed**: pandas is not installed on this machine.
      The claim is unverified, not verified-false.
- [ ] Output filename convention matches the legacy one (`benchmark{Model}{YYYYMMDD}.csv`) —
      the harness writes `bench_<engine>_<model>_<timestamp>.csv`. Left unticked.
- [x] Schema drift detected by a test comparing emitted headers to the notebooks' expectations —
      `TestBuildRowsPairedContract`, `TestBuildRowsSingleContract`,
      `TestRunDryRunWritesEveryColumn`, `TestWriteReportKeepsInputColumns`

### Baselines (currently missing)
- [x] **F§4.5** — regex baseline: `eval|exec|subprocess|shell=True|pickle|yaml.load` etc.
- [x] TF-IDF + shallow classifier baseline on the same corpus
- [x] A "do nothing" baseline (always vulnerable / always safe) for reference
- [x] On CVE→CWE classification, a TF-IDF + shallow classifier scored 74% vs LLM prompting 59%
      (ACM 10.1145/3733800.3763267) — if the baseline is competitive, that is the honest finding

### Corpora
- [x] `bench/data1551redacted.csv` — 1,551 paired rows; runner for the paired schema
- [ ] `bench/train/` — 1,673 vulnerable + 1,379 patched files; runner for the single-sample
      schema — the harness supports the single-sample **schema**, but there is no runner that
      walks the `bench/train/` directory and builds a CSV from it. Left unticked.
- [x] Support both input schemas (paired and single) as the legacy notebooks do
- [x] Label normalization identical to the legacy `_normalize_benchmark_label`

### Metrics
- [x] Precision, recall, F1, and **recall-weighted F2/F3** — not just F1
- [x] ROC-AUC where the model produces a usable score — implemented and unit-tested; it is
      reported as **not applicable** because no engine currently emits a continuous score
- [x] Confusion matrix
- [x] Per-CWE breakdown (the legacy work added per-type metrics)
- [x] **FPR on the patched corpus** reported explicitly and prominently

### External benchmarks
- [x] **RealVuln** adapter (realvuln.com, arXiv:2604.13764)
  - [x] 1,443 Python vulns, 280 curated FP traps, 73 CWEs — the shipped corpus holds **1,899
        vulnerabilities and 283 traps**, not the 1,443/280 of the paper; the numbers of the
        manifest were confirmed against the adapter's own counts
  - [x] Matching semantics: file path + CWE from `acceptable_cwes` + line within **±10**
  - [ ] Compare against the 16 published scanner baselines — not done; the published numbers
        were not obtained, so no comparison is claimed
- [x] **OWASP BenchmarkPython** adapter (1,230 cases, per-case TP/FP labels, GPL-3.0)
  - [x] Keep the corpus out of the shipped binary; fetch at benchmark time
- [x] Keep the adapters in a separate package so the licensing stays clean

### Leakage control
- [ ] Report results on a **post-cutoff split** where the model's training cutoff predates the
      CVEs — the split exists (`bench.SplitByCutoff`) but the corpus carries **no date column**,
      so it can only run on a proxy ordering, which is named as a proxy in the API and in the
      docs and is never presented as a true post-cutoff split. Left unticked.
- [ ] Document the cutoff of each evaluated model — the cutoff cannot be inferred; the code
      requires the operator to supply it. Left unticked.
- [x] Out-of-distribution evaluation dropped models from 0.776 → 0.519 in
      arXiv:2507.21817 — dataset choice dominates results. The figure in this plan is **not**
      in the abstract of that paper; the verified abstract pair (0.703 → 0.493, BigVul) is
      cited instead and the plan's figure is attributed rather than asserted.

### Reproducibility
- [x] `make bench` runs everything from a clean checkout
- [x] Results are committed alongside the run metadata (model, seed, prompt version, corpus hash)
      — `bench/baseline_metrics.json` holds a real run; the metrics JSON carries the prompt
      version, the corpus path, the corpus SHA-256 and the engine model
- [x] Deterministic: the same inputs produce the same rows

## Acceptance criteria

- [ ] The existing notebooks run unmodified against the Go harness output — the schema matches,
      but the notebooks were not executed (no pandas on this machine)
- [x] The benchmark reports trivial baselines alongside the LLM
- [x] Recall-weighted metrics are reported, not only F1
- [x] RealVuln results are reproducible from a clean checkout — the corpus is fetched with a
      pinned URL and a recorded SHA-256, and the offline mode reproduces identical numbers
- [x] The published app configuration is the configuration that was benchmarked

## Notes

- **This is the credibility workstream.** The legacy notebooks compare Bandit vs LLM with no
  trivial baseline, on a private dataset, with a scoring scheme (exact snippet match) that can
  look good while measuring nothing. Fixing that turns AISAI from a demo into a *reference
  implementation* for local LLM auditing of Python — a category with no reference implementation
  today.
- The legacy `_bandit_valid_issues` accepted MEDIUM **and** HIGH severity **and** confidence,
  while the app ran `-lll` (HIGH only). The benchmark was measuring a different tool.
- The legacy `label_from_prediction` / `expected_before` / `expected_after` semantics are kept
  so historical result files stay comparable.

### Measured in this pass (2026-11-21)

**Static engine (`internal/sast`, `aisai-sast(min-severity=low)`) over the bundled corpus,
1,551 paired rows = 3,102 samples.** Real output of
`aisai-bench --engine bandit --metrics-out …`:

```
samples 3102, decided 3102, abstention 0
confusion      tp 156, fp 112, tn 1439, fn 1395
accuracy 51.4%   precision 58.2%   recall 10.1%
f1 / f2 / f3   17.2% / 12.1% / 11.0%
roc-auc        n/a (no engine emits a continuous score)
patched fpr    7.2% (112 false positives over 1551 decided patched samples)
null baseline  always_vulnerable 50.0%, always_safe 50.0%
```

**The headline number is the null baseline.** The engine scores 51.4% accuracy against a 50.0%
floor: 1.4 points above a coin flip, with a recall of 10.1%. It finds 156 of 1,551
vulnerabilities and wrongly flags 112 of 1,551 already-patched snippets. The corpus's
vulnerability families are largely absent from the ten AST rules.

**TF-IDF + multinomial Naive Bayes, held-out split** (1,315 train rows, 236 held-out rows,
seed 20240517, zero shared snippets between the splits):

```
held-out  accuracy 51.3%  precision 51.4%  recall 45.3%  F1 48.2%
keyword control, same split: accuracy 50.0%
```

The trivial baseline is **1.3 points above a coin flip and no better than the shipped rule
engine**. The published 74% of ACM 10.1145/3733800.3763267 was measured on CVE *description
text*, not on source code; on source code the signal is far weaker. This is the honest finding
and it is reported rather than hidden.

**RealVuln v3.1.1** (66 Python repositories, 3,448 files, corpus SHA-256
`2c903dcf…1bd960`) — live run of the static engine:

```
TP 147, FP 153, FN 1752, TN 282 — precision 49.0%, recall 7.7%, F1 13.4%
traps 283, hit 1
```

The manifest counts (1,899 vulnerabilities, 283 traps) match the adapter's own counts exactly,
which confirms the ground truth loaded completely.

**OWASP BenchmarkPython** (1,230 cases, corpus SHA-256 `0defda4c…29096`) — live run:

```
TP 40, FP 125, FN 412, TN 778 — precision 24.2%, recall 8.8%, F1 13.0%
```

**The LLM path is unmeasured.** No provider was reachable (no Ollama on this machine), so
there is **no** LLM number in this workstream. The LLM engine is wired and unit-tested against
a stub; it has never been measured against the corpus. Any statement about the LLM's accuracy
would be invented, so none is made.

**ROC-AUC is not applicable.** The metric is implemented (rank-based Mann–Whitney, exact
mid-rank ties, cross-checked against brute force) and the API accepts scores, but
`bench.Outcome` carries a class rather than a score, so no score reaches the report. Closing
this needs a score field on the outcome.

**The legacy filename convention was not adopted.** `bench_<engine>_<model>_<timestamp>.csv`
is written instead of `benchmark{Model}{YYYYMMDD}.csv`.

**The `bench/train/` directory has no runner.** The single-sample schema is supported by the
harness, but nothing walks the directory of 5,831 files and builds the CSV.

**A defect in the nightly comparison script was found and fixed.** The script computed the
precision from the "before" column only, whose ground truth is always "vulnerable" in a paired
corpus, so it reported a precision of 1.0. The false positives are in the "after" column — the
patched snippets — and the script now counts them there. The committed baseline was regenerated
with the corrected script and agrees with the Go metrics exactly (precision 58.21%, recall
10.06%, FPR 7.22%).
