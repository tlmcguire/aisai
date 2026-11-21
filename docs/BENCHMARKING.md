# AISAI Benchmarking

This document explains how to reproduce the AISAI benchmark numbers, what the corpus
contains, what the output CSV must look like for the existing notebooks to read it, and
— most importantly — what the headline number actually means, including the parts of it
that are unflattering.

## The headline number, stated honestly

On the full 1,551-pair corpus in `bench/data1551redacted.csv`, the static rule engine
scores:

| Measurement | Value |
|---|---|
| Correct classifications | **1,595 / 3,102** |
| Accuracy | **51.4%** |
| Null baseline (always-vulnerable or always-safe) | **50.0%** |

3,102 is 1,551 rows × 2 snippets: every row contributes one vulnerable snippet
("before") and one fixed snippet ("after"), so the engine is asked 3,102 questions.

The corpus is **balanced** between vulnerable and patched code: each row supplies
exactly one of each, so a detector that always answers "vulnerable" and one that always
answers "safe" both score exactly 50.0%. The static engine's 51.4% is **1.4 percentage
points above a coin flip**.

This is a deliberately unflattering measurement, and it is published as measured. The
explanations below are the hypotheses, not excuses.

The accuracy alone does not say what kind of "barely above chance" this is. The
[metrics](#metrics) of the same run do: the engine is **precise** (58.2%) and
**useless** (10.1% recall) — it fires on a small minority of the vulnerable snippets
and stays quiet on 92.8% of the patched code. That is a coverage problem, not a
coin-flip detector, and the difference matters for what to do about it.

### Why the number is near chance

**The corpus does not match the rules.** The 1,551 rows are real-world
web-application and library vulnerabilities drawn from a multi-project corpus — Django
is the single largest family (1,028 rows import `django`), followed by TensorFlow (556),
rdiffweb (555), zenml, nautobot, synapse, mlflow, Flask, CherryPy, Twisted and Vyper.
The *vulnerability* families are dominated by web-application patterns. The most
common are:

| Rows | Family |
|---|---|
| 256 | Insufficient Information (`NVD-CWE-noinfo` — no specific pattern) |
| 125 | Cross-site scripting (XSS) |
| 77 | Open redirect |
| 77 | Path traversal |
| 59 | CSRF |
| 52 | Resource allocation without limits |
| 51 | SSRF |
| 47 | Insufficient session expiration |
| 45 | Improper input validation |

The ten AST rules in `internal/sast/rules.go` target a different set of constructs
entirely:

| Rule | CWE | What it needs |
|---|---|---|
| `subprocess-shell-true` | CWE-78 | `subprocess` with `shell=True` |
| `eval-exec-nonliteral` | CWE-95 | `eval` / `exec` / `compile` on a non-literal |
| `pickle-yaml-unsafe-load` | CWE-502 | `pickle.load`/`loads`, `yaml.load` without `SafeLoader` |
| `tempfile-mktemp` | CWE-377 | `tempfile.mktemp` |
| `weak-hash-md5-sha1` | CWE-327 | MD5 / SHA1 |
| `hardcoded-secret` | CWE-798 | a high-entropy literal on a secret-ish name |
| `debug-true-enabled` | CWE-489 | `DEBUG = True` |
| `assert-used-for-validation` | CWE-617 | `assert` used for validation |
| `tls-verify-disabled` | CWE-295 | `verify=False` |
| `path-traversal` | CWE-22 | a path derived from a request parameter |

Nine of those ten families are absent from the corpus's top fifteen. A corpus of CSRF,
XSS and open-redirect fixes exercises none of them, so a rule engine that reports
"vulnerable" only when it sees one of its own patterns will, correctly, report "safe"
for almost everything. The score is measuring **coverage mismatch**, not random
guessing.

The one family that does overlap is path traversal (77 rows, CWE-22), and the
`path-traversal` rule is the one rule that needs request-parameter awareness — which a
syntactic rule can only approximate.

**The next measurement to make** is the notebook's per-CWE-family summary. Both CSV
notebooks contain `summarize_per_vulnerability_family`, which groups the rows by CWE
family (using `vulnerability_type_cwe_ids` and `vulnerability_type_names`) and reports
per-family accuracy, pair accuracy and average scan time. Run the static engine over the
corpus, point that function at the output CSV, and look at the `Injection`,
`Deserialization`, `Crypto / Secrets` and `Path Traversal / File Access` rows. Those are
the families the rules *should* score on. If the rule engine is better than chance
there and at chance elsewhere, the engine is fine and the corpus is the problem. If it
is at chance there too, the rules need work.

### F§1.4: why the legacy numbers and the app disagreed

This number exists partly because of a defect in the legacy app, recorded as **F§1.4**
in `NOTES_improvements.md`:

> **1.4 Bandit severity filter in the app ≠ Bandit config in the benchmark**
> - App: `bandit ... -lll` plus `issue_severity == 'high'` → only HIGH.
> - Benchmark: `_bandit_valid_issues` accepts **MEDIUM or HIGH** severity **and** confidence.
>
> → **The product ships a configuration the benchmark never measures.**

The legacy app ran Bandit with `-lll` (HIGH only) while its own benchmark notebook
accepted MEDIUM *and* HIGH findings. The two disagreed about the same code, so a
benchmark number described a tool the user never had. Bandit's MEDIUM findings — the
`try/except/pass` and `assert`-in-production cases — were invisible in the app while
the benchmark counted them.

**The rewrite fixes this by construction.** The benchmark calls the same
`internal/sast` code the app calls, through `bench.StaticEngine`, with the same
severity policy (`bench.SeverityPolicy` = `model.SeverityLow`, "count every finding the
engine reports"). There is **no severity filter at the engine level** — the severity
filter in the findings API is a read-time filter that never changes what the engine
reports. The `bandit_` column prefix survives only because that is the prefix the
notebooks look for; the values are produced by `internal/sast`, not by Bandit. Nothing
is shelled out.

So the 51.4% is a number about the shipped product. It is unflattering precisely
because it can no longer be inflated by a filter the product does not use.

### The LLM path was not measured

**The LLM path could not be measured in the build environment: no Ollama is available
there.** There is no local model server, and the BYOK path needs an API key that the
environment does not have. No LLM benchmark result is claimed, implied or estimated in
this document.

The harness supports it fully — `--engine llm` runs `internal/scan`'s detect→verify
pipeline over the corpus — but a full LLM run over the corpus costs 3,102 detection
calls plus one verification call per candidate finding, so it needs a real model and a
lot of wall-clock time.

What *is* covered is the classification semantics, by unit tests in
`internal/bench/bench_test.go` against a fake provider:

| Situation | Classification | `completed` | Test |
|---|---|---|---|
| A candidate the verifier confirms | `vulnerable` | `true` | `TestLLMEngineVerifiedFindingIsVulnerable` |
| The model abstains (`abstained: true`) | `safe` | `true` | `TestLLMEngineAbstentionIsSafe` |
| A candidate the verifier rejects as a false positive | `safe` | `true` | `TestLLMEngineDroppedCandidateIsSafe` |
| Detection fails (model unreachable, timeout, schema violation) | `unknown` | `false` | `TestLLMEngineHardErrorIsUnknown` |
| Detection succeeds but verification fails | `unknown` | `false` | `TestLLMEngineVerificationErrorIsUnknown` |

The distinction matters and is preserved in the CSV: **an abstention is a legitimate
`safe`**, because the engine answered and found nothing. **A hard error is a legitimate
`unknown`**, because the engine could not answer at all. The notebooks treat
`unknown` as its own confusion-matrix column and count rows whose `completed` column is
not `True` as failed tests. An abstention must never be reported as a failure, and a
failure must never be reported as an abstention — otherwise a flaky model silently
inflates the score.

Note that the LLM engine deliberately drives `Detect` and `Verify` separately instead of
calling `DetectAndVerify`, because `DetectAndVerify` keeps a candidate whose
verification failed and records the failure in the verifier note. That is right for the
application (the user should still see the finding) but wrong for a benchmark, which has
to tell an abstention apart from a broken run.

## The corpus

| Path | Contents |
|---|---|
| `bench/data1551redacted.csv` | 1,551 paired rows. Header: `cve_id, vulnerability_type_cwe_ids, vulnerability_type_names, severity, exploitability_score, impact_score, cvss2_access_complexity, cvss3_attack_complexity, file_cyclomatic_complexity, commit_dmm_complexity, hash, filename, change_type, programming_language, vulnerable_code, fixed_code`. All 1,551 rows are `Python`. Cells contain embedded newlines, so the file must be read as RFC 4180 CSV, not line by line. |
| `bench/train/vulnerable/` | 1,673 `.py` files plus 1,516 `.bak` copies — the vulnerable side of the training corpus. |
| `bench/train/patched/` | 1,379 `.py` files plus 1,258 `.bak` copies — the patched side. |
| `bench/train/` | **5,831 files** in total (both directories, `.py` and `.py.bak`). |
| `bench/results/` | The output CSVs of previous runs. |

The `bench/train/` tree is the raw material for few-shot examples and for the legacy
notebooks that walk the directories; `data1551redacted.csv` is what `aisai-bench`
consumes. Do not delete either — they are hard to regenerate.

## The harness

`cmd/aisai-bench` is the entry point; `internal/bench` holds the logic. The harness
calls the same engine code the application uses — `internal/sast` for the static engine
and `internal/scan` for the LLM engine — and shells out to nothing.

```
make build           # builds bin/aisai-server, bin/aisai-mcp, bin/aisai-bench
```

### Flags

| Flag | Default | Meaning |
|---|---|---|
| `--input` | `bench/data1551redacted.csv` | the corpus CSV to read |
| `--out` | `bench/results/bench_<engine>_<tag>_<timestamp>.csv` | where to write the result |
| `--engine` | `both` when an LLM is reachable, else `bandit` | `llm`, `bandit` (also accepted: `static`, `sast`), or `both` |
| `--baseline` | — | run a trivial baseline instead of an engine: `always_vulnerable`, `always_safe` or `keyword`. No model is used. |
| `--dry-run` | `false` | make no model call: the LLM path is exercised with a stub classifier. When `--engine` is not given, a dry run uses both engines. |
| `--limit` | `0` | maximum number of rows to evaluate. `0` means every row. |
| `--before-col` | `vulnerable_code` | the column holding the vulnerable snippet |
| `--after-col` | `fixed_code` | the column holding the fixed snippet |
| `--code-col` | — | the snippet column, for single-mode input |
| `--label-col` | — | the label column, for single-mode input |
| `--model` | the configured scan model | the model name for the LLM engine |
| `--concurrency` | `4` | number of workers of the LLM path |
| `--model-tag` | — | a string embedded in the default output file name |
| `--config` | — | the config file to load |
| `--metrics` | `true` | print the detection metrics of every engine to stderr |
| `--no-metrics` | `false` | do not print the detection metrics |
| `--metrics-out` | — | path of the JSON file the metrics are written to. Implies the metrics are computed, even with `--no-metrics`. |

### Recipes

The full static run that produced the published number:

```
bin/aisai-bench --input bench/data1551redacted.csv --engine bandit \
    --out bench/results/bench_bandit_sast.csv
```

A fast sanity check (no model, 50 rows, static engine only):

```
make bench-quick
# bin/aisai-bench --input bench/data1551redacted.csv --engine bandit --limit 50 --dry-run \
#     --out bench/results/bench-quick.csv
```

A dry run that exercises the whole pipeline including the LLM column layout, with a
stub classifier in place of the model:

```
bin/aisai-bench --input bench/data1551redacted.csv --dry-run \
    --out bench/results/bench-dry-run.csv
```

The trivial baselines:

```
bin/aisai-bench --input bench/data1551redacted.csv --baseline always_vulnerable \
    --out bench/results/baseline-always-vulnerable.csv
bin/aisai-bench --input bench/data1551redacted.csv --baseline always_safe \
    --out bench/results/baseline-always-safe.csv
bin/aisai-bench --input bench/data1551redacted.csv --baseline keyword \
    --out bench/results/baseline-keyword.csv
```

Both trivial baselines score exactly 50.0% on this corpus, because the corpus is
balanced. If you ever see a baseline score that is not 50.0%, the corpus or the
harness has changed — check before drawing a conclusion.

The LLM run, once a model is reachable:

```
bin/aisai-bench --input bench/data1551redacted.csv --engine llm \
    --out bench/results/bench_llm_<model>.csv
```

Note that `make bench-baseline` is currently broken: it passes `--baseline` with no
value to a flag that requires one. Run the baseline commands by hand until that is
fixed.

### The trivial baselines

| Baseline | Behaviour | Model column |
|---|---|---|
| `always_vulnerable` | always answers `vulnerable` | `baseline:always_vulnerable` |
| `always_safe` | always answers `safe` | `baseline:always_safe` |
| `keyword` | a regex heuristic over `eval(`, `exec(`, `pickle.loads`, `os.system`, `subprocess(..., shell=True)` and `yaml.load` without `SafeLoader` | `baseline:keyword` |

The `keyword` baseline is deliberately naive: `os.system(['ls'])` counts as vulnerable
however it is used. It exists to show what a pattern matcher that does not look at
control flow can do.

A baseline run is written under the `llm_` prefix (the `bandit_` prefix belongs to the
static engine) and its model column always starts with `baseline:`, so a baseline can
never be mistaken for a real engine run.

## The output CSV contract

The notebooks under `bench/notebooks/` are unmodified and must stay that way. The
harness writes a CSV they can read as-is.

The output keeps **every column of the input** and appends the metric columns. That is
what lets the per-vulnerability-family analysis keep working: it reads
`vulnerability_type_cwe_ids` and `vulnerability_type_names`.

Appended columns, in order:

1. `benchmark_input_mode` — `paired` or `single`.
2. `expected_before` / `expected_after` (paired mode) or `expected_label` (single mode).
   In paired mode these are always `vulnerable` and `safe`.
3. The metric columns of each engine, prefixed with the engine's prefix.

Paired-mode metric columns, for prefix `p` (either `bandit_` or `llm_`):

```
p_classification_before      p_classification_after
p_correct_before             p_correct_after
p_success_before             p_success_after
p_completed_before           p_completed_after
p_time_before                p_time_after
p_scan_time_before_seconds   p_scan_time_after_seconds
p_pair_correct
p_model
```

Single-mode metric columns:

```
p_classification   p_correct   p_success   p_completed
p_time             p_scan_time_seconds
p_expected_label   p_code_column   p_label_column
p_model
```

Details that matter:

- The `p_` prefix is `bandit` for the static engine and `llm` for the LLM engine. The
  name `bandit` is historical: the notebooks look for that prefix. The values come from
  `internal/sast`.
- `p_classification_*` is one of `vulnerable`, `safe` or `unknown`.
- `p_correct_*` / `p_success_*` / `p_completed_*` are spelled `True` / `False` — pandas
  reads those as booleans; `true` and `1` are not read as booleans and the notebooks
  would break.
- `p_time_*` and `p_scan_time_*_seconds` carry the same value, rounded to three
  decimals. The `_time_` names are the task contract; the `_scan_time_seconds` names are
  what the notebooks read.
- `p_pair_correct` is `True` when both the before and the after snippet of that row
  were classified correctly.
- In a `both` run the `llm_` columns are written before the `bandit_` columns.

### A discrepancy to be aware of

The Go source comments in `cmd/aisai-bench/main.go` and `internal/bench/csv.go` say the
notebooks pick their metric prefix with `startswith("llm_")`. They do not. The actual
helper, `_benchmark_metric_prefix` in both CSV notebooks, is:

```python
def _benchmark_metric_prefix(df):
    if any(column.startswith("bandit_") for column in df.columns):
        return "bandit"
    if any(column.startswith("llm_") for column in df.columns):
        return "llm"
    return None
```

It checks `bandit_` **first**. So in a `both` run the notebook summarises the **static**
engine, not the LLM engine, regardless of column order. The column order is still worth
keeping — the harness's own `TestEngineOrderPutsLLMFirst` asserts it, and
`TestRunDryRunWritesEveryColumn` depends on it — but the notebooks will not switch
engines based on it. **To summarise the LLM engine, write a CSV that contains only the
`llm_` columns.** The comments in the Go source are wrong on this point; they should be
corrected.

## Reading a result with the notebooks

The notebooks are the analysis layer; the harness only produces the CSV.

```
# bench/notebooks/staticBenchmarkCSV.ipynb  — the static engine over a CSV
# bench/notebooks/llmBenchmarkCSV.ipynb      — the LLM engine over a CSV
```

Both notebooks detect the input layout themselves with `_detect_benchmark_input_schema`
(mirrored in Go by `bench.DetectSchema`), read the engine columns with
`_benchmark_result_mode` and `_build_benchmark_report_data`, and produce the
per-vulnerability-family summary with `summarize_per_vulnerability_family`.

To summarise a saved run, set `prior_results_csv_path` in the notebook to the CSV the
harness wrote and run the summary cell:

```python
prior_results_csv_path = "bench/results/bench_bandit_sast_20261121-110707.csv"
```

The `llmBenchmark.ipynb` and `staticBenchmark.ipynb` (non-CSV variants) walk
`bench/train/` directly with the legacy Python code. They are kept for reference and
for the `bench/train/` corpus; they are not part of the rewrite's measurement path.

## Reading a result by hand

The harness prints a summary line per engine when it finishes:

```
aisai-bench: bandit: 1595/3102 correct (51.4%) over the completed evaluations
```

Only *completed* evaluations are counted. A row whose engine failed is excluded from
the numerator and the denominator, and shows up in the CSV as
`completed = False` with an `unknown` classification. To reproduce the count from the
CSV:

```python
import pandas as pd
df = pd.read_csv("bench/results/bench_bandit_sast_20261121-110707.csv")
done = df[df["bandit_completed_before"] & df["bandit_completed_after"]]
correct = done["bandit_correct_before"].sum() + done["bandit_correct_after"].sum()
total = 2 * len(done)
print(f"{correct}/{total} = {correct/total:.1%}")
```

The harness prints the detection metrics to stderr right after that line: the
confusion matrix, the precision, the recall, F1, F2, F3, the abstention count and
rate, the false-positive rate on the patched corpus and the null baselines. See
[Metrics](#metrics).

## Metrics

The accuracy line above is one number out of many, and for a security tool it is the
least useful one: a detector that answers `safe` to everything scores 50.0% on this
corpus. `internal/bench/metrics.go` computes the full set, and the harness prints it to
**stderr** after every run, so that stdout stays clean.

The metrics are additive. The CSV the notebooks read is not changed by any of this:
the column schema is exactly the one documented above.

### The metrics

The positive class is **vulnerable**. A sample is *decided* when the engine completed
the run and answered `vulnerable` or `safe`. Every metric below is computed over the
decided samples only.

| Metric | Definition | Zero denominator |
|---|---|---|
| Confusion matrix | `tp`, `fp`, `tn`, `fn` over the decided samples | — |
| Accuracy | `(tp + tn) / decided` | `0` |
| Precision | `tp / (tp + fp)` | `0` — the engine flagged nothing |
| Recall | `tp / (tp + fn)` | `0` — there is no vulnerable sample to find |
| F1 | `2·tp / (2·tp + fp + fn)` | `0` |
| F2 | `(1+β²)·P·R / (β²·P + R)`, β = 2 | `0` |
| F3 | `(1+β²)·P·R / (β²·P + R)`, β = 3 | `0` |
| ROC-AUC | rank-based (Mann–Whitney U) over the engine's scores | **not applicable** — see below |
| Abstention | the samples the engine did not answer: count and rate | — |
| Patched FPR | the patched samples called vulnerable, over the decided patched samples | `0` |
| Per-CWE | the same metrics, per vulnerability family, with the support of each family | — |

A metric with a zero denominator is reported as **`0`** — never as `NaN`, never as
"undefined". An engine that flags nothing has a precision of zero, not an unknown
precision. The one exception is the ROC-AUC, which is *not applicable* when there is no
score to compute it from.

### Why F2 and F3

F1 weighs a missed vulnerability and a false alarm equally. That is the wrong balance
for a vulnerability auditor, so the harness also reports F2 and F3, which weight the
recall twice and three times as much as the precision:

```
Fβ = (1 + β²) · P · R / (β² · P + R)
```

A higher β pulls the score towards the recall. When the recall is below the precision,
`F3 ≤ F2 ≤ F1`; when the recall is above the precision, the order is reversed.

The choice is deliberate and it is a trade-off, not a free lunch. **A missed
vulnerability is worse than a false alarm**, because the user of an auditor acts on
what they are told: a missed vulnerability is a silent hole in the supply chain. **But
false alarms have a real cost too**: a scanner that cries wolf on every file gets
ignored, and then its true findings are ignored with it. F1 hides that trade-off
behind a single number; F2 and F3 make it explicit.

### Unknown and abstention

`unknown` is a real class of the output contract — the notebooks give it its own
confusion-matrix column — so it is **never folded into `safe`**.

A sample the engine did not answer is *abstained*. That is the case when the run did
not complete (the model was unreachable, the request timed out, the snippet could not
be parsed) or when the classification is `unknown`. Abstained samples are:

- **excluded** from the confusion matrix and from every metric derived from it;
- **counted** in the abstention count, with the number of `unknown` classifications
  and the number of failed runs broken out;
- **reported as a rate** over all the samples the engine was asked about.

Folding them into `safe` would inflate the accuracy of an engine that simply gives up,
and it would hide the failure rate behind a plausible number. An engine that abstains
on everything has a precision of 100% and a patched FPR of 0% — and only the
abstention count tells the reader that the engine never answered.

The harness prints the abstention count and rate next to the metrics they qualify, on
every run.

### The false-positive rate on the patched corpus

For the paired layout the patched side of the corpus is the `after` snippet of every
row — the code after the fix. Those samples are the ones the tool cries wolf on, and
the rate it does so at is the number that decides whether the tool is used at all:

```
patched FPR = (patched samples the engine called vulnerable) / (patched samples the engine decided)
```

Abstained patched samples are excluded from the denominator, exactly as they are
excluded from the confusion matrix, and the numerator and the denominator are both
reported so that the rate can be checked.

In single mode there is no "after" snippet: the same rate is then computed over the
samples whose ground truth is `safe`, and `patched_corpus.applicable` is false when
the corpus holds no such sample.

The patched FPR is printed on its own line in the per-engine block, and it is in the
JSON under `patched_corpus`.

### The null baselines

The two trivial classifiers are evaluated over the same samples as the engine, so that
the numbers can be read against the floor:

- `always_vulnerable` — every sample is called vulnerable;
- `always_safe` — every sample is called safe.

The per-engine line is computed over the samples that engine decided; the corpus-wide
line is computed over every sample of the corpus. On the balanced paired corpus both of
them score exactly **50.0%**, whichever way round they are put. Any detector that does
not beat 50.0% is not detecting anything. The baselines are printed under every engine
and again for the corpus as a whole.

### The measured numbers

The full-corpus static run, measured with `--engine bandit`:

| Metric | Value |
|---|---|
| Confusion matrix | `tp 156`, `fp 112`, `tn 1439`, `fn 1395` |
| Accuracy | **51.4%** |
| Precision | **58.2%** (156 of the 268 flagged snippets) |
| Recall | **10.1%** (156 of the 1,551 vulnerable snippets) |
| F1 | **17.2%** |
| F2 | **12.1%** |
| F3 | **11.0%** |
| ROC-AUC | **not applicable** — the engine produces no score |
| Abstention | 0 samples (0.0%) |
| Patched FPR | **7.2%** (112 false positives over 1,551 decided patched snippets) |
| Null baseline | always-vulnerable **50.0%**, always-safe **50.0%** |

The accuracy is barely above the floor, but the breakdown says why, and it is not the
same story as the accuracy alone. The engine is **precise** (58.2%) and **useless**
(10.1% recall): it fires on a minority of the vulnerable snippets and it keeps quiet
most of the time, so it cries wolf on only 7.2% of the patched code. In other words,
the 51.4% is not a coin flip dressed up as a detector — it is a detector that almost
never fires at all. That is the honest reading of the number, and it is a coverage
problem, not a noise problem.

### Reproducing

```
bin/aisai-bench --input bench/data1551redacted.csv --engine bandit \
    --out bench/results/bench_bandit_sast.csv \
    --metrics-out bench/results/bench_bandit_sast.metrics.json
```

The metrics are printed to stderr; the JSON file holds the same numbers in a
machine-readable shape:

```json
{
  "schema_version": "1",
  "input_mode": "paired",
  "corpus": { "rows": 1551, "samples": 3102, "vulnerable_samples": 1551, "safe_samples": 1551 },
  "engines": [
    {
      "engine": "bandit",
      "model": "aisai-sast(min-severity=low)",
      "samples": 3102, "decided": 3102, "unlabeled": 0,
      "confusion_matrix": { "tp": 156, "fp": 112, "tn": 1439, "fn": 1395 },
      "accuracy": 0.5142, "precision": 0.5821, "recall": 0.1006,
      "f1": 0.1715, "f2": 0.1205, "f3": 0.1097,
      "roc_auc": null,
      "roc_auc_reason": "the engine produces no continuous score: the outcome carries a class, not a score",
      "abstention": { "count": 0, "rate": 0, "unknown": 0, "failed": 0 },
      "per_cwe": [ { "cwe": "CWE-22", "support": 160, "decided": 160, "..." : "..." } ],
      "patched_corpus": { "applicable": true, "samples": 1551, "decided": 1551, "false_positives": 112, "fpr": 0.0722 },
      "null_baselines": { "always_vulnerable": { "accuracy": 0.5 }, "always_safe": { "accuracy": 0.5 } }
    }
  ],
  "null_baselines": { "always_vulnerable": { "accuracy": 0.5 }, "always_safe": { "accuracy": 0.5 } }
}
```

`roc_auc` is `null` when the ROC-AUC is not applicable, and never `0.0` and never
`0.5`.

The per-CWE breakdown groups the rows by `vulnerability_type_cwe_ids`, the same
column the notebooks group by. A row that carries several families — the corpus holds
`CWE-20,CWE-918` and similar — contributes its samples to each of them, so the
supports of the groups add up to more than the size of the corpus. A row whose family
cell is empty or holds a placeholder such as `N/A` falls into the `(unspecified)`
group.

### Known gaps

- **The ROC-AUC is not applicable.** The metric is implemented
  (`bench.ROCAUC`, rank-based with exact tie handling, checked against a brute-force
  count in `internal/bench/metrics_test.go`) and the harness carries the scores when
  an engine produces them (`bench.ComputeEngineMetricsWithScores`), but **no engine of
  the harness produces a continuous score today**: `bench.Outcome` carries a class, not
  a score, and the CSV schema has no score column. The `tfidf` baseline already
  computes a probability for the vulnerable class internally
  (`TFIDFModel.Predict`); carrying it through to the report needs a score field on
  `bench.Outcome`, which lives in `internal/bench/csv.go`. Until that exists the
  ROC-AUC is reported as not applicable, and it is **not** reported as 0.0 or 0.5.
- **The abstention rate is measured over the samples, not over the time.** An engine
  that answers slowly and an engine that answers quickly are counted the same way; the
  timings stay in the CSV columns.
- **The per-CWE groups overlap**, by construction: a row with several families is
  counted in each of them. The supports are there so that the overlap is visible.
- **The metrics are computed on the corpus as it is.** There is no post-cutoff split
  and no leakage control in the harness yet; the `tfidf` baseline guards its own
  training split, the other engines have nothing to memorise.

## Reference results

| Run | File | Result |
|---|---|---|
| Static engine, full corpus, 2026-11-21 | `bench/results/bench_bandit_sast_20261121-110707.csv` | 1,595 / 3,102 = **51.4%** (156 correct on the vulnerable side, 1,439 on the fixed side) |
| Static engine, 50-row dry run | `bench/results/bench-quick.csv` | 50 / 100 = 50.0% |

The published artifact for the full run is `bench/results/bench_bandit_sast_20261121-110707.csv`.
Its model column reads `aisai-sast(min-severity=low)`, which is the severity policy the
application uses.

The metrics of the full-corpus run quoted in [Metrics](#metrics) were measured with the
same command on the same corpus:

```
bin/aisai-bench --input bench/data1551redacted.csv --engine bandit \
    --out /tmp/bench-full-metrics.csv --metrics-out /tmp/bench-full-metrics.json
```

The confusion matrix it produced — `tp 156`, `fp 112`, `tn 1439`, `fn 1395` — is the
same 156 correct vulnerable snippets and 1,439 correct patched snippets the accuracy line
above reports, split into the four cells. The output was written outside the repository:
`bench/results/` is not written by this pass.

## See also

- [`docs/ARCHITECTURE.md`](ARCHITECTURE.md) — the "one engine, two harnesses" rule.
- [`docs/plan/w9-benchmark.md`](plan/w9-benchmark.md) — the work plan for this area.
