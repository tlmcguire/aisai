# AISAI external benchmarking

This document covers the three things the benchmark workstream added next to the
internal corpus: a trivial-method baseline, the leakage control that says
which rows a model could not have seen, and the two external benchmarks the
project can be measured against.

It is a companion of [`BENCHMARKING.md`](BENCHMARKING.md), not a replacement.
That document explains the internal corpus and the headline number; this one
explains what else is measured, how, and what could not be measured.

Every number below was produced by the commands shown next to it. Nothing here
is estimated, extrapolated or taken from a paper.

## The honest summary

| Measurement | Value |
|---|---|
| TF-IDF + Naive Bayes, held-out split | **51.27%** accuracy (236 rows) |
| TF-IDF keyword control, held-out split | **50.00%** accuracy (236 rows) |
| Null baseline (always-vulnerable or always-safe) | **50.00%** |
| Static rule engine, full internal corpus | 51.4% accuracy ([`BENCHMARKING.md`](BENCHMARKING.md)) |
| RealVuln, static engine | precision 49.0%, recall 7.7%, F1 13.4% |
| OWASP BenchmarkPython, static engine | precision 24.2%, recall 8.8%, F1 13.0% |

Two of these numbers matter more than the others.

**The trivial baseline is competitive.** A TF-IDF model with a shallow
classifier, trained on a fifth of the corpus, reaches 51.3% on a held-out
fifth — within noise of the rule engine's 51.4% and 1.3 points above a coin
flip. The published result that motivates this baseline found TF-IDF scoring
74% against 59% for LLM prompting on CVE→CWE classification
([doi:10.1145/3733800.3763267](https://doi.org/10.1145/3733800.3763267)). That
was on CVE *description text*, whose schema is so regular that keywords carry
the answer. On Python *source code*, over this corpus, the same trick buys
almost nothing. Both facts are true, and the second one is the one that
applies here.

**The external benchmarks are much harder.** On RealVuln and on the OWASP
benchmark the same engine finds fewer than one vulnerability in ten, and on the
OWASP benchmark four findings in five are on the wrong place. This is not a
regression against the internal corpus: it is what the internal corpus cannot
show, because it asks a yes-or-no question about a snippet and never asks
where the vulnerability is.

## The TF-IDF baseline

### What it is

`internal/bench/tfidf.go` implements, in the standard library only:

- **A tokeniser.** Identifiers are split on underscores and on camel-case
  humps, so `parseHTTPResponse` yields `parse`, `http` and `response` and
  `os.system` yields `os` and `system`. Operators and keywords are tokens of
  their own, and the contents of a string literal are tokenised like any other
  text. Tokens are lower-cased.
- **A TF-IDF featuriser.** The vocabulary is built from the training split
  only, with a minimum document frequency of 2 and a ceiling of 60,000
  features. The inverse document frequency is smoothed, the term frequency is
  the count over the tokens of the snippet, and the vector is L2-normalised so
  that a long snippet and a short one are comparable.
- **A shallow classifier.** A multinomial Naive Bayes over those features,
  with additive smoothing of 0.1.

### Why Naive Bayes and not a logistic regression

The first implementation was a logistic regression trained with stochastic
gradient descent, which is the other obvious choice. It was dropped because it
does not converge on this corpus: the two classes are barely separable, the
intercept random-walks, and the classifier flips between answering
"vulnerable" for everything and "safe" for everything from one epoch to the
next. Measured over a sweep of learning rates and epoch counts, the held-out
recall swung between 0.4% and 98.7% while the held-out accuracy never left the
50–53% band. A number that depends on the moment it is asked at is not a
measurement.

Naive Bayes has no trajectory. The same corpus and the same seed always give
the same model and the same number.

### The split, and the leakage discipline

The split is deterministic and is part of the contract of the baseline:

- The rows are **grouped by the `hash` column**, the commit the row comes
  from. The corpus has 1,551 rows and 742 distinct hashes, so a split that
  cut between rows would put the same commit on both sides.
- The groups are ordered by `sha256(seed + group key)` and the first groups
  that fill the test fraction become the held-out split. The result depends on
  the content of the rows and on the seed, and on nothing else — not on the
  order of the rows.
- A group that shares a snippet with a group of the training split is moved
  into the training split, and the rule is applied until it holds. Two
  different commits can carry the same snippet; this removes that leak. The
  measured number of shared snippets is **0**.
- The default seed is `20240517` and the default test fraction is 0.2. Both
  are parameters (`bench.SplitOptions`), and both are recorded in the result.

**The evaluation never touches the training split.** The classifier is trained
on the vulnerable and the fixed snippet of the training rows; the evaluation
counts the vulnerable and the fixed snippet of the held-out rows. A snippet
the model has seen is refused rather than scored: `TFIDFModel.ClassifyOutcome`
answers `unknown` for it, so an in-sample result can never be read as a
held-out one.

The pairing is what makes the baseline answer the question it is there to
answer. For a row of the corpus, the vulnerable snippet is expected to be
vulnerable and the fixed snippet is expected to be safe. A classifier trained
on one half of the rows and evaluated on the other half measures exactly
"would a trivial method have found this too?".

### The two modes

| Mode | What it does |
|---|---|
| `classifier` | The TF-IDF features and the trained Naive Bayes classifier. |
| `keyword` | The same tokeniser, but the decision is made by a fixed list of dangerous API tokens: `eval`, `exec`, `compile`, `pickle`, `mktemp`, `md5`, `sha1`, or the co-occurrence of `os` and `system`, of `subprocess` and `shell`, of `yaml` and `load`, of `verify` and `false`. No training, no vocabulary. |

The keyword mode is the degenerate control. It isolates the contribution of
the classifier: both modes share the featurisation, so the difference between
them is the learned model and nothing else.

### The measured result

```bash
cd /Users/tyler/Projects/aisai
export PATH="/usr/local/go/bin:$PATH"
go test ./internal/bench/ -run 'TestTFIDF' -v
```

```
rows: 1551 (train 1315, test 236)
seed: 20240517, test fraction: 0.20, group column: hash
snippets shared between the splits: 0
vocabulary: 39149 features
held-out: TP 107, FP 101, FN 129, TN 135
held-out accuracy: 0.5127, precision 0.5144, recall 0.4534, F1 0.4820
in-sample accuracy (training split): 0.5890
```

```
keyword mode, held-out: TP 82, FP 82, FN 154, TN 154
keyword mode, held-out accuracy: 0.5000, precision 0.5000, recall 0.3475, F1 0.4100
```

The in-sample accuracy is 58.9% against 51.3% held out: the model overfits by
about seven points, which is what the in-sample number is there to show.

The result is stable across seeds (`TestTFIDFIsStableAcrossSeeds`):

| Seed | Test rows | Accuracy | Precision | Recall | F1 | In-sample |
|---|---|---|---|---|---|---|
| 1 | 247 | 0.5223 | 0.5266 | 0.4413 | 0.4802 | 0.5897 |
| 7 | 228 | 0.5351 | 0.5449 | 0.4254 | 0.4778 | 0.5907 |
| 20240517 | 236 | 0.5127 | 0.5144 | 0.4534 | 0.4820 | 0.5890 |
| 424242 | 269 | 0.5186 | 0.5240 | 0.4052 | 0.4570 | 0.5917 |

**What this means.** The trivial method is 1.3 to 3.5 points above a coin
flip, and no better than the rule engine that ships. The published 74% does not
transfer to source code. Anyone reading a higher AISAI number on this corpus
should read it next to this one.

### How to run the baseline through the harness

The baseline is registered in the baseline-name switch of
`bench.NewBaselineEngine`, next to `always_vulnerable`, `always_safe` and
`keyword`:

```bash
go run ./cmd/aisai-bench --baseline tfidf --limit 50 --out /tmp/bench-tfidf.csv
```

The model has to be trained and installed first, because the harness has no
way of carrying a trained model through its options:

```go
model, split, err := bench.TrainTFIDFBaseline("bench/data1551redacted.csv", bench.TFIDFOptions{})
```

`TrainTFIDFBaseline` reads the corpus, splits it, trains on the training split
and installs the model. `bench.CurrentTFIDFModel()` returns it,
`bench.SetTFIDFModel(nil)` removes it, and `bench.EvaluateTFIDF` produces the
held-out numbers of the table above.

**A caveat about the CSV.** A run of the baseline over the whole corpus writes
the training rows as `unknown`, because they are not evaluated. The accuracy
of such a CSV is therefore meaningless: use `EvaluateTFIDF`, not the
notebooks, for this baseline.

## The post-cutoff split, and what it is not

`internal/bench/leakage.go` splits the corpus into a pre-cutoff and a
post-cutoff group, so that a result can be reported on the rows that postdate
the training cutoff of the evaluated model.

### The corpus has no date

The corpus of this project has no date column. Its columns are the CVE
identifier, the vulnerability family, the severities, the complexity metrics,
the commit hash, the file name, the change type, the language and the two
snippets. The only date-like signal is the **year in the CVE identifier**,
which is the year the identifier was assigned.

**Every name of the API says this is a proxy.** The function is
`SplitByCutoffProxy`, the constant is `CutoffProxyCVEIDYear`, and the result
carries `IsProxy: true` and a `ProxyKind`. The metadata of a run carries a
note that says the same thing in words. A split that used a real date column
would set `IsProxy: false` and `ProxyKind: CutoffProxyDateColumn`; the
function `SplitByCutoff` picks that path automatically when the caller names
a date column the corpus actually has.

The proxy is conservative. The derived date of a row is the first moment of
its year, so a row whose year is the year of the cutoff is put in the
**pre-cutoff** group: its real date may be before the cutoff, and the
post-cutoff group should stay clean.

### The cutoffs are supplied by the operator

Neither cutoff can be inferred, and neither is inferred:

- **The cutoff of the corpus** is a choice of the operator.
- **The training cutoff of the evaluated model** is a fact about the model.
  No API of a model provider states it, and a benchmark that guessed it would
  be measuring nothing. It is a parameter (`bench.CutoffOptions.ModelCutoff`),
  it is recorded in the run metadata, and it is compared with the derived
  dates of the rows.

`CutoffSplit.ContaminatedRows()` reports how many rows of the post-cutoff
group are still inside the training data of the model — the rows whose derived
date is before the model's training cutoff. When the model was trained later
than the corpus cutoff, part of the post-cutoff group is contaminated, and the
number says how much.

### Why this matters more than the number

The lesson of [arXiv:2507.21817](https://arxiv.org/abs/2507.21817) (*Out of
Distribution, Out of Luck*, ICSE 2026) is that in-distribution performance
does not predict out-of-distribution performance: a model trained on BigVul
reached the highest in-distribution accuracy, 0.703, and fell to 0.493 out of
distribution, while a model trained on their TitanVul data reached only 0.590
in distribution and 0.881 out of distribution. The dataset choice dominates
the result.

The workstream brief cites the same paper for a 0.776 → 0.519 drop on
out-of-distribution evaluation. That pair is not in the abstract; the abstract's
own example is the 0.703 → 0.493 one above, and the direction is the same.
Either way the conclusion is the one that matters here: **the corpus you
evaluate on dominates the number you report**, which is the reason the
post-cutoff split exists and the reason it is a documented proxy rather than a
convenient one.

### The metrics of a group

`CutoffSplit.Metrics(report, engine)` returns the metrics of each group —
`pre-cutoff`, `post-cutoff` and `undated` — over the rows of that group
only, so a run can be reported on the post-cutoff subset alone.

## RealVuln

### What it is

[RealVuln](https://github.com/kolega-ai/Real-Vuln-Benchmark) (arXiv:2604.13764,
Apache-2.0) is a benchmark of real-world vulnerabilities in real repositories.
The Python part of it, as of the version this adapter was written against:

| | |
|---|---|
| Benchmark version | 3.1.1 (released 2026-11-18) |
| Python repositories | 66 |
| Python ground-truth entries | 2,182 |
| — vulnerabilities | 1,899 |
| — false-positive traps | 283 |
| Distinct primary CWEs (Python) | 81 |
| Distinct acceptable CWEs (Python) | 146 |

The workstream brief quotes ~1,443 Python vulnerabilities, 280 traps and 73
CWEs. Those are the figures of an earlier release; the corpus has grown, and
the numbers above are the ones the adapter actually read. The count of 1,899
vulnerabilities and 283 traps is the manifest's own, and the adapter's counts
agree with it — a check that the ground truth was loaded completely.

### How to fetch it

```bash
go run ./cmd/aisai-bench-external --corpus realvuln --cache-dir .aisai-bench-cache
```

The corpus is **not** vendored into this repository. It is fetched at benchmark
time into the cache directory:

- The benchmark archive comes from a pinned URL that addresses one immutable
  commit of the benchmark repository, so the bytes never change under a
  running benchmark.
- The 66 target repositories come from their own pinned commit, taken from the
  manifest, so the sources always match the ground truth.
- The digest of the benchmark archive is recorded and written into the report.
  The manifest's own `ground_truth_content_hash` pins the content of the
  ground-truth files independently of the archive.

`--offline` reads only what the cache already holds and fails with a clear
error when something is missing.

### The matching semantics

A detection matches a ground-truth entry when **all three** hold:

1. the **file path** matches, after the normalisation of the corpus
   (backslashes become forward slashes, a leading `./` or `/` is stripped,
   the comparison is case sensitive);
2. the **CWE** of the finding is in the `acceptable_cwes` set of the entry;
3. the reported **line** lies within the line range of the entry, widened by
   **10 lines** on each side.

These are the semantics of the benchmark's own scorer
(`scorer/matcher.py` of the benchmark repository), not a simplification of
them. The consequences:

- A finding on a line exactly 10 lines away matches; a finding 11 lines away
  does not. Both boundaries are unit-tested
  (`TestRealVulnMatchesEntryToleranceBoundaries`).
- The window is the line *range* of the entry widened by the tolerance, not
  the tolerance around its start line: an entry on lines 100–110 accepts a
  finding on line 120 and refuses one on line 121.
- An entry may carry **acceptable locations**: other places in the repository
  where the same weakness appears. A finding on one of them matches, with the
  same file and line semantics and the same CWE gate.
- An entry marked `non_scoring` is a location whose status the benchmark
  leaves unsettled. It takes **no part** in the matching: reporting on it is
  not a false positive and missing it is not a false negative. The check is
  deliberately location-only, so that a finding is not withheld by a CWE the
  benchmark never settled.
- When several entries match one finding, the **vulnerable** one wins, so
  that a tool is not penalised by a trap that sits next to a real
  vulnerability.
- An entry matches at most one finding, so one lucky finding cannot claim
  two vulnerabilities.

The matching is a pure function over in-memory records
(`external.MatchRealVuln`), and it is tested with hand-built fixtures: path
match, CWE set membership, the ±10 window and its boundaries, the wrong path,
the wrong CWE, the traps, the acceptable locations, the non-scoring entries
and the preference of the vulnerable entry.

### The measured result

```bash
go run ./cmd/aisai-bench-external --corpus realvuln --cache-dir .aisai-bench-cache --out /tmp/rv.json
```

```
engine: aisai-sast(min-severity=low)
files_scanned: 3448
findings: 300
true_positives: 147
false_positives: 153
false_negatives: 1752
true_negatives: 282
precision: 0.490
recall: 0.0774
f1: 0.1337
false_positive_traps: {traps: 283, hit: 1, silent: 282}
corpus_sha256: 2c903dcfe3a67a8b935d023b4377f6e35cdb1df45f4b212b6b43f4329a1bd960
```

**What this means.** The rule engine found 147 of the 1,899 Python
vulnerabilities — 7.7% — and it was right about half of what it reported. It
fell into one of the 283 curated false-positive traps. This is a much harder
measurement than the internal corpus, and it is the honest one: the internal
corpus asks whether a snippet looks vulnerable, and these repositories ask
whether a whole application is understood well enough to place a finding on
the right line of the right file with the right CWE.

The numbers above are the static engine alone. No LLM provider was reachable
from the environment the adapter was written in, so the LLM path of the
external benchmarks has **not** been measured.

## OWASP BenchmarkPython

### What it is

The [OWASP Benchmark for Python](https://github.com/OWASP-Benchmark/BenchmarkPython)
is a test suite of 1,230 deliberately vulnerable Python files, each with its
expectations. It is licensed **GPL-3.0**, which is the reason it is not
vendored into this repository and not shipped in any binary of this project:
it is fetched at benchmark time, into a cache directory that is not part of
the repository.

### How to fetch it

```bash
go run ./cmd/aisai-bench-external --corpus benchmarkpython --cache-dir .aisai-bench-cache
```

The archive comes from a pinned URL that addresses one immutable commit, and
its digest is recorded in the report. The expected results are read from the
file the benchmark's own scoring configuration names
(`scoringconfigfile.yaml`).

### The matching semantics

The benchmark's test cases are **whole files**, not snippets, so the adapter
runs the static engine over a whole file and maps the finding's file path,
line and CWE onto the expectations of the case.

Both layouts of the expected results are supported:

- The **per-line** layout has the columns `test name, category, real
  vulnerability, cwe, filename, line, issue`, where the issue is `TP` or `FP`
  for one line of the test file.
- The **file-level** layout, which is what the current release ships, has the
  columns `test name, category, real vulnerability, cwe` and no line. The
  verdict is then a property of the whole file.

The rules:

- A finding on a line the case expects to be reported is a **true positive**.
- A finding on a line the case expects to stay silent on, or on a line the
  case says nothing about, is a **false positive**: the benchmark expects the
  tool to be quiet there.
- A finding whose CWE is another weakness than the one the case expects is a
  **false positive**, and the expectation stays missed. The CWE gate only
  applies when both sides declare a CWE.
- An expected line that no finding met is a **false negative**; a case that
  expects no finding and got none is a **true negative**.

### The measured result

```bash
go run ./cmd/aisai-bench-external --corpus benchmarkpython --cache-dir .aisai-bench-cache --out /tmp/bp.json
```

```
engine: aisai-sast(min-severity=low)
files_scanned: 1230
findings: 165
true_positives: 40
false_positives: 125
false_negatives: 412
true_negatives: 778
precision: 0.242
recall: 0.0885
f1: 0.1297
corpus_sha256: 0defda4cce2ea7675fbeae5b059b4d7cca7d49232529367133feb1adfb529096
```

**What this means.** 452 of the 1,230 cases hold a real vulnerability, and the
engine found 40 of them. It reported 165 findings, 125 of which are wrong.
The corpus is dominated by web-application weaknesses — cross-site scripting,
SQL injection, open redirect — and by construction the ten rules of
`internal/sast` do not cover them; that is the same coverage mismatch
[`BENCHMARKING.md`](BENCHMARKING.md) describes, seen from the outside.

## The external CLI

```
usage: aisai-bench-external [flags]

  --corpus string        corpus to run: realvuln | benchmarkpython
  --cache-dir string     directory the corpora are cached in (default ".aisai-bench-cache")
  --offline              do not download anything: use only what the cache already holds
  --limit int            maximum number of files to scan; 0 means every file
  --out string           path of the JSON report (default: stdout)
  --engine string        engine to run: static | llm | both
  --model string         name of the LLM model
  --language string      language of the RealVuln repositories to run (default "python")
  --cutoff string        cutoff date of the corpus, as YYYY-MM-DD
  --model-cutoff string  training cutoff of the evaluated model, as YYYY-MM-DD
  --config string        path of the config file
```

The results go to stdout, or to the file of `--out`. The logs go to stderr.

The report carries the schema version, the name of the corpus, the digest of
the artefact the corpus was read from, and, per engine, the number of files
scanned, the number of findings, the true positives, the false positives, the
false negatives, the true negatives, the precision, the recall, the F1, the
outcome on the false-positive traps (RealVuln) and the files that could not
be scanned.

The LLM engine is added when a provider answers its health probe, and the
static engine always runs. The provider construction is the same one
`cmd/aisai-bench` uses: the same configuration, the same BYOK handling and
the same local Ollama fallback.

## Reproducing everything

```bash
export PATH="/usr/local/go/bin:$PATH"
cd /Users/tyler/Projects/aisai

# The unit tests, including the matching semantics of both corpora.
go test ./internal/bench/... -v

# The TF-IDF baseline, on the held-out split of the internal corpus.
go test ./internal/bench/ -run 'TestTFIDF' -v

# The external benchmarks. The first run downloads them into the cache.
go run ./cmd/aisai-bench-external --corpus realvuln --cache-dir .aisai-bench-cache --out /tmp/rv.json
go run ./cmd/aisai-bench-external --corpus benchmarkpython --cache-dir .aisai-bench-cache --out /tmp/bp.json

# The same runs without any network access.
go run ./cmd/aisai-bench-external --corpus realvuln --offline --out /tmp/rv.json
```

The cache directory holds tens of megabytes. It is not part of the repository
and should not be committed; add it to `.gitignore` if the default location is
used.

## Known gaps

These are the gaps of this work, stated plainly.

1. **The LLM engine was not measured on either external corpus.** No model
   provider was reachable from the environment the adapters were written in.
   The code path exists, is wired to `internal/scan` and is exercised by the
   same harness as the static engine, but **no number for it exists**, and
   none is reported here. The line numbers of the LLM path are a known rough
   edge: the chunk of the application is framed with absolute file lines,
   while the external corpora match on absolute lines and the model answers
   with the line of the snippet. The adapter reframes the chunk so that the
   numbers the model reports are relative to the chunk and maps them back to
   absolute file lines itself. That is a deliberate deviation from the
   application's framing and it is documented in `external.LLMTDetector`.

2. **The post-cutoff split is a proxy, not a date.** The corpus has no date
   column. The year of the CVE identifier is used, the API says so, and the
   limitation is repeated in every report the split produces. A corpus with a
   real date column would use `SplitByCutoff` with `DateColumn` set, and the
   result would set `IsProxy: false`.

3. **The training cutoff of a model is never inferred.** It is supplied by the
   operator and recorded. A run without it cannot say anything about
   contamination, and `ContaminatedRows` returns zero in that case rather
   than pretending the group is clean.

4. **The RealVuln corpus is a moving target.** The adapter pins the benchmark
   archive and the target repositories to immutable commits, so a number
   cannot change under a running benchmark. But the corpus grows: the
   figures in this document are the ones of version 3.1.1, and the counts the
   brief quotes are those of an earlier release. A number measured against
   another version is not comparable to these, and the report carries the
   digest so that the difference is visible.

5. **The OWASP benchmark is scored with the file-level layout only.** The
   current release of the expected results carries no line, so the verdict is
   a property of the whole file and the line of a finding only decides
   whether it is in the file at all. The per-line layout is implemented and
   unit-tested, but it is not exercised by a live run, because the corpus
   does not ship it.

6. **The TF-IDF baseline was not compared with the LLM engine on the same
   split.** The baseline is measured against the held-out split of the
   internal corpus; the LLM engine is measured over the corpus as a whole by
   `cmd/aisai-bench`. Putting the two side by side needs the LLM run to be
   made on the same rows, which the harness does not offer yet.

7. **The benchmark harness cannot carry a trained model.** The TF-IDF
   baseline is installed into the package with `bench.SetTFIDFModel` rather
   than passed through `bench.Options`, because the options and the run path
   of the harness are owned by another part of the workstream. The wiring
   works and is tested; it is not the shape the harness would have if it were
   free to change.

8. **The baselines of the internal corpus have no external counterpart.** The
   `always_vulnerable`, `always_safe` and `keyword` baselines of
   `cmd/aisai-bench` are not implemented for the external corpora, where the
   question is not "vulnerable or safe" but "here or not here".

9. **The cache directory is not in `.gitignore`.** The default location of the
   corpora is `.aisai-bench-cache` in the working directory, and the
   repository does not ignore it. The ignore rule belongs to the repository
   configuration, which this work does not own; until it is added, run the
   benchmark with `--cache-dir` pointing outside the tree, or do not commit
   the cache directory.
