package bench

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync/atomic"
	"unicode"
)

// The defaults of the TF-IDF baseline.
const (
	// DefaultSplitSeed is the seed of the deterministic corpus split.
	//
	// The seed is part of the contract of the baseline: a number that is
	// not reproducible is not a number.
	DefaultSplitSeed int64 = 20240517
	// DefaultTestFraction is the share of the corpus groups that goes to
	// the held-out split.
	DefaultTestFraction = 0.2
	// DefaultGroupColumn is the column the rows are grouped by.
	//
	// The corpus repeats the same commit in several rows — 1,551 rows
	// carry 742 distinct hashes — so a split that cuts between the rows
	// of one commit puts the same code in both splits. The group column
	// keeps the rows of one commit together.
	DefaultGroupColumn = "hash"
	// DefaultTFIDFSmoothing is the additive smoothing of the classifier.
	DefaultTFIDFSmoothing = 0.1
	// tfidfMinDocumentFrequency is the smallest number of training
	// documents a token must appear in to enter the vocabulary.
	tfidfMinDocumentFrequency = 2
	// tfidfMaxVocabulary bounds the size of the vocabulary.
	tfidfMaxVocabulary = 60000
)

// The modes of the TF-IDF baseline.
const (
	// TFIDFModeClassifier trains a shallow classifier on the TF-IDF
	// features of the training split.
	TFIDFModeClassifier = "classifier"
	// TFIDFModeKeyword is the degenerate control: the same tokenisation,
	// but the decision is made by a fixed list of dangerous API tokens
	// instead of a trained model. It exists to make the contribution of
	// the classifier visible separately from the featurisation.
	TFIDFModeKeyword = "keyword"
)

// Tokenize splits Python source into the tokens the baseline is built on.
//
// Identifiers are split on the underscores and on the camel-case humps, so
// that "parseHTTPResponse" yields "parse", "http" and "response" and that
// "os.system" yields "os" and "system". Operators and keywords are kept as
// tokens of their own, and the contents of a string literal are tokenized
// like any other text.
//
// The tokens are lower-cased: the corpus is Python, and a case-sensitive
// vocabulary would only add noise.
func Tokenize(code string) []string {
	var tokens []string
	runes := []rune(code)
	i := 0
	for i < len(runes) {
		r := runes[i]
		switch {
		case r == '"' || r == '\'':
			// A string literal: its content is tokenized like text, and
			// a marker token records that a literal was there.
			j := i + 1
			for j < len(runes) && runes[j] != r {
				if runes[j] == '\\' {
					j++
				}
				j++
			}
			content := string(runes[i+1 : min(j, len(runes))])
			tokens = append(tokens, "str")
			tokens = append(tokens, splitWords(content)...)
			if j < len(runes) {
				j++
			}
			i = j
		case unicode.IsLetter(r) || r == '_':
			j := i
			for j < len(runes) && (unicode.IsLetter(runes[j]) || unicode.IsDigit(runes[j]) || runes[j] == '_') {
				j++
			}
			tokens = append(tokens, splitIdentifier(string(runes[i:j]))...)
			i = j
		case unicode.IsDigit(r):
			j := i
			for j < len(runes) && (unicode.IsDigit(runes[j]) || runes[j] == '.') {
				j++
			}
			tokens = append(tokens, "num")
			i = j
		case unicode.IsSpace(r):
			i++
		default:
			op := string(r)
			if i+1 < len(runes) {
				two := string(runes[i : i+2])
				switch two {
				case "==", "!=", "<=", ">=", "->", "**", "//", "+=", "-=", "*=", "/=", "<<", ">>", "|>":
					op = two
					i++
				}
			}
			tokens = append(tokens, op)
			i++
		}
	}
	return tokens
}

// splitIdentifier splits an identifier on its underscores and its camel-case
// humps.
func splitIdentifier(id string) []string {
	var parts []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			parts = append(parts, strings.ToLower(cur.String()))
			cur.Reset()
		}
	}
	runes := []rune(id)
	for i, r := range runes {
		if r == '_' {
			flush()
			continue
		}
		if unicode.IsUpper(r) && i > 0 {
			prev := runes[i-1]
			next := rune(0)
			if i+1 < len(runes) {
				next = runes[i+1]
			}
			boundary := unicode.IsLower(prev) || unicode.IsDigit(prev)
			if unicode.IsUpper(prev) && unicode.IsLower(next) {
				// The last capital of a run starts the next word.
				boundary = true
			}
			if boundary {
				flush()
			}
		}
		cur.WriteRune(r)
	}
	flush()
	return parts
}

// splitWords splits a text into lower-cased words.
func splitWords(text string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, strings.ToLower(cur.String()))
			cur.Reset()
		}
	}
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			cur.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return out
}

// Document is one labelled snippet of the corpus.
type Document struct {
	// Code is the source text.
	Code string
	// Label is the ground truth: ClassificationVulnerable or
	// ClassificationSafe.
	Label string
}

// SplitOptions configures the deterministic split of the corpus.
type SplitOptions struct {
	// Seed is the seed the split is derived from. The default is
	// DefaultSplitSeed.
	Seed int64
	// TestFraction is the share of the groups that goes to the held-out
	// split. The default is DefaultTestFraction.
	TestFraction float64
	// GroupColumn is the column the rows are grouped by. The default is
	// DefaultGroupColumn.
	GroupColumn string
}

// Split is a deterministic partition of the corpus rows.
//
// The split is deterministic: the same corpus and the same seed always give
// the same partition, and the partition does not depend on the order of the
// rows.
type Split struct {
	// Train holds the row indices of the training split.
	Train []int
	// Test holds the row indices of the held-out split.
	Test []int
	// Seed is the seed the split was derived from.
	Seed int64
	// TestFraction is the share of the groups that went to the held-out
	// split.
	TestFraction float64
	// GroupColumn is the column the rows were grouped by.
	GroupColumn string
	// SharedSnippets is the number of snippets that appear in both
	// splits.
	//
	// It is a leakage diagnostic. The group column keeps the rows of one
	// commit together, but two different commits can still carry the
	// same code; the count says how many snippets are in that situation,
	// and it is reported rather than hidden.
	SharedSnippets int
}

// SplitRows partitions the rows of a corpus.
//
// The rows are grouped by the group column, so that the rows of one commit
// never straddle the split. The groups are ordered by the digest of their
// key and the seed, and the first groups that fill the test fraction become
// the held-out split. The result therefore depends on the content of the
// rows and on the seed, and on nothing else.
func SplitRows(table *Table, opts SplitOptions) (*Split, error) {
	if table == nil {
		return nil, fmt.Errorf("bench: no corpus to split")
	}
	if opts.Seed == 0 {
		opts.Seed = DefaultSplitSeed
	}
	if opts.TestFraction <= 0 || opts.TestFraction >= 1 {
		opts.TestFraction = DefaultTestFraction
	}
	if opts.GroupColumn == "" {
		opts.GroupColumn = DefaultGroupColumn
	}

	groups := map[string][]int{}
	for i := range table.Rows {
		key := table.Cell(i, opts.GroupColumn)
		if strings.TrimSpace(key) == "" {
			// A row without a group key is grouped by its identity, so
			// that it still lands on one side of the split.
			key = fmt.Sprintf("%s#%d", table.Cell(i, "cve_id"), i)
		}
		groups[key] = append(groups[key], i)
	}

	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(a, b int) bool {
		return groupOrder(keys[a], opts.Seed) < groupOrder(keys[b], opts.Seed)
	})

	total := len(table.Rows)
	target := int(float64(total) * opts.TestFraction)

	// The groups are assigned to the held-out split first.
	heldOut := map[string]bool{}
	assigned := 0
	for _, k := range keys {
		rows := groups[k]
		if assigned < target {
			heldOut[k] = true
			assigned += len(rows)
		}
	}

	// A group whose snippets also appear in another group of the training
	// split would put the same code on both sides of the split. The
	// grouping by commit does not prevent that: two different commits can
	// carry the same snippet. Such a group is moved to the training
	// split, so that no snippet is evaluated on data the model has seen.
	heldOut = dropSharedGroups(table, groups, heldOut)

	split := &Split{
		Seed:         opts.Seed,
		TestFraction: opts.TestFraction,
		GroupColumn:  opts.GroupColumn,
	}
	for _, k := range keys {
		if heldOut[k] {
			split.Test = append(split.Test, groups[k]...)
			continue
		}
		split.Train = append(split.Train, groups[k]...)
	}
	sort.Ints(split.Test)
	sort.Ints(split.Train)

	split.SharedSnippets = countSharedSnippets(table, split)
	return split, nil
}

// dropSharedGroups removes from the held-out split every group that shares a
// snippet with a group of the training split.
//
// The removal is repeated until the held-out split holds no shared snippet
// any more, because moving a group to the training split can put a snippet
// there that another held-out group also carries.
func dropSharedGroups(table *Table, groups map[string][]int, heldOut map[string]bool) map[string]bool {
	for {
		training := map[string]bool{}
		for k, rows := range groups {
			if heldOut[k] {
				continue
			}
			for _, row := range rows {
				for _, code := range []string{
					table.Cell(row, DefaultBeforeCol),
					table.Cell(row, DefaultAfterCol),
				} {
					if code == "" {
						continue
					}
					training[snippetFingerprint(code)] = true
				}
			}
		}
		moved := false
		for k, rows := range groups {
			if !heldOut[k] {
				continue
			}
			shared := false
			for _, row := range rows {
				for _, code := range []string{
					table.Cell(row, DefaultBeforeCol),
					table.Cell(row, DefaultAfterCol),
				} {
					if code == "" {
						continue
					}
					if training[snippetFingerprint(code)] {
						shared = true
						break
					}
				}
				if shared {
					break
				}
			}
			if shared {
				delete(heldOut, k)
				moved = true
			}
		}
		if !moved {
			return heldOut
		}
	}
}

// groupOrder returns the sort key of a group.
func groupOrder(key string, seed int64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", seed, key)))
	return hex.EncodeToString(sum[:])
}

// countSharedSnippets counts the snippets that appear in both splits.
func countSharedSnippets(table *Table, split *Split) int {
	train := map[string]bool{}
	for _, row := range split.Train {
		for _, code := range []string{
			table.Cell(row, DefaultBeforeCol),
			table.Cell(row, DefaultAfterCol),
		} {
			if code == "" {
				continue
			}
			train[snippetFingerprint(code)] = true
		}
	}
	shared := 0
	for _, row := range split.Test {
		for _, code := range []string{
			table.Cell(row, DefaultBeforeCol),
			table.Cell(row, DefaultAfterCol),
		} {
			if code == "" {
				continue
			}
			if train[snippetFingerprint(code)] {
				shared++
			}
		}
	}
	return shared
}

// snippetFingerprint returns the fingerprint of a snippet.
func snippetFingerprint(code string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(code)))
	return hex.EncodeToString(sum[:])
}

// TFIDFOptions configures the TF-IDF baseline.
type TFIDFOptions struct {
	// Split configures the deterministic split of the corpus.
	Split SplitOptions
	// Mode is TFIDFModeClassifier or TFIDFModeKeyword. The default is
	// TFIDFModeClassifier.
	Mode string
	// Smoothing is the additive smoothing of the classifier. The default
	// is DefaultTFIDFSmoothing.
	Smoothing float64
}

// TFIDFModel is the trained TF-IDF baseline.
//
// A model is immutable once trained, and Predict is safe for concurrent use.
type TFIDFModel struct {
	// mode is the mode the model was trained in.
	mode string
	// vocab maps a token to its column of the weight vector.
	vocab map[string]int
	// idf holds the inverse document frequency of every column.
	idf []float64
	// vulnerableWeights holds the log-likelihood of every column for the
	// vulnerable class.
	vulnerableWeights []float64
	// safeWeights holds the log-likelihood of every column for the safe
	// class.
	safeWeights []float64
	// bias is the difference of the log-priors of the two classes.
	bias float64
	// train holds the fingerprints of the snippets the model was trained
	// on. A snippet of the training split is never evaluated: it would
	// measure the model against itself.
	train map[string]bool
	// split is the split the model was trained on.
	split *Split
	// vocabularySize is the number of columns of the weight vector.
	vocabularySize int
}

// TrainTFIDF trains the baseline on the documents given.
//
// The documents are the training split of the corpus: the vulnerable snippet
// of a row is a document labelled vulnerable, and the fixed snippet of the
// same row is a document labelled safe.
//
// The classifier is a multinomial Naive Bayes over the TF-IDF features, with
// additive smoothing.
//
// The choice of Naive Bayes over a logistic regression trained with
// stochastic gradient descent is deliberate. On this corpus the two classes
// are barely separable, and a gradient descent on such a signal does not
// converge: the intercept random-walks, and the classifier flips between
// answering "vulnerable" for everything and "safe" for everything from one
// epoch to the next. The accuracy of such a model is a function of the
// moment it is asked at, which makes it useless as a measurement. Naive
// Bayes has no trajectory: the same corpus and the same seed always give the
// same model and the same number.
func TrainTFIDF(docs []Document, opts TFIDFOptions) *TFIDFModel {
	if opts.Mode == "" {
		opts.Mode = TFIDFModeClassifier
	}
	if opts.Smoothing <= 0 {
		opts.Smoothing = DefaultTFIDFSmoothing
	}

	m := &TFIDFModel{
		mode:  opts.Mode,
		train: map[string]bool{},
	}
	for _, d := range docs {
		m.train[snippetFingerprint(d.Code)] = true
	}

	if opts.Mode == TFIDFModeKeyword {
		return m
	}

	m.vocab, m.idf = buildVocabulary(docs)
	m.vocabularySize = len(m.vocab)

	// The weight of every feature for every class, and the prior of every
	// class.
	vulnerable := make([]float64, m.vocabularySize)
	safe := make([]float64, m.vocabularySize)
	var vulnerableTotal, safeTotal float64
	var vulnerableDocs, safeDocs int

	for _, d := range docs {
		x := m.vector(d.Code)
		if d.Label == ClassificationVulnerable {
			vulnerableDocs++
			for _, f := range x {
				vulnerable[f.col] += f.weight
				vulnerableTotal += f.weight
			}
			continue
		}
		safeDocs++
		for _, f := range x {
			safe[f.col] += f.weight
			safeTotal += f.weight
		}
	}

	alpha := opts.Smoothing
	denominatorVulnerable := vulnerableTotal + alpha*float64(m.vocabularySize)
	denominatorSafe := safeTotal + alpha*float64(m.vocabularySize)
	m.vulnerableWeights = make([]float64, m.vocabularySize)
	m.safeWeights = make([]float64, m.vocabularySize)
	for col := 0; col < m.vocabularySize; col++ {
		m.vulnerableWeights[col] = math.Log((vulnerable[col] + alpha) / denominatorVulnerable)
		m.safeWeights[col] = math.Log((safe[col] + alpha) / denominatorSafe)
	}

	totalDocs := vulnerableDocs + safeDocs
	if totalDocs > 0 {
		m.bias = math.Log(float64(vulnerableDocs)/float64(totalDocs)) -
			math.Log(float64(safeDocs)/float64(totalDocs))
	}
	return m
}

// feature is one non-zero entry of a TF-IDF vector.
type feature struct {
	// col is the column of the vocabulary.
	col int
	// weight is the TF-IDF weight of the feature.
	weight float64
}

// buildVocabulary builds the vocabulary and the inverse document frequency
// of the training documents.
func buildVocabulary(docs []Document) (map[string]int, []float64) {
	df := map[string]int{}
	for _, d := range docs {
		seen := map[string]bool{}
		for _, tok := range Tokenize(d.Code) {
			if !seen[tok] {
				seen[tok] = true
				df[tok]++
			}
		}
	}
	tokens := make([]string, 0, len(df))
	for tok, n := range df {
		if n < tfidfMinDocumentFrequency {
			continue
		}
		tokens = append(tokens, tok)
	}
	// The order of the vocabulary is fixed by the token itself, so that
	// the same corpus always yields the same weights.
	sort.Strings(tokens)
	if len(tokens) > tfidfMaxVocabulary {
		tokens = tokens[:tfidfMaxVocabulary]
	}
	vocab := make(map[string]int, len(tokens))
	idf := make([]float64, len(tokens))
	for i, tok := range tokens {
		vocab[tok] = i
		idf[i] = math.Log((1+float64(len(docs)))/(1+float64(df[tok]))) + 1
	}
	return vocab, idf
}

// vector returns the TF-IDF vector of a snippet.
//
// The vector is sparse: it holds the columns of the tokens the snippet
// actually uses, with their TF-IDF weight. The weights are L2-normalised,
// which is what makes a long snippet and a short one comparable.
func (m *TFIDFModel) vector(code string) []feature {
	tokens := Tokenize(code)
	if len(tokens) == 0 {
		return nil
	}
	counts := map[int]float64{}
	total := 0
	for _, tok := range tokens {
		col, ok := m.vocab[tok]
		if !ok {
			continue
		}
		counts[col]++
		total++
	}
	if total == 0 {
		return nil
	}
	cols := make([]int, 0, len(counts))
	for col := range counts {
		cols = append(cols, col)
	}
	sort.Ints(cols)
	out := make([]feature, 0, len(cols))
	var norm float64
	for _, col := range cols {
		w := (counts[col] / float64(total)) * m.idf[col]
		out = append(out, feature{col: col, weight: w})
		norm += w * w
	}
	if norm > 0 {
		norm = math.Sqrt(norm)
		for i := range out {
			out[i].weight /= norm
		}
	}
	return out
}

// Mode returns the mode the model was trained in.
func (m *TFIDFModel) Mode() string { return m.mode }

// Split returns the split the model was trained on, or nil.
func (m *TFIDFModel) Split() *Split { return m.split }

// SplitDescription describes the configuration of the model in a form that
// fits in a CSV cell.
//
// The description carries the seed and the size of the split, because a
// baseline that was trained on a different split is a different baseline: a
// result that does not say which split produced it cannot be reproduced.
func (m *TFIDFModel) SplitDescription() string {
	if m == nil {
		return ""
	}
	parts := []string{"mode=" + m.mode}
	if m.split != nil {
		parts = append(parts,
			fmt.Sprintf("seed=%d", m.split.Seed),
			fmt.Sprintf("train=%d", len(m.split.Train)),
			fmt.Sprintf("test=%d", len(m.split.Test)),
			fmt.Sprintf("group=%s", m.split.GroupColumn),
		)
	}
	return strings.Join(parts, " ")
}

// VocabularySize returns the number of features the classifier uses.
func (m *TFIDFModel) VocabularySize() int { return m.vocabularySize }

// IsTrainingSnippet reports whether a snippet is one the model was trained
// on.
//
// Such a snippet must not be evaluated: the model has seen it, and the
// number it produces would measure the model against itself.
func (m *TFIDFModel) IsTrainingSnippet(code string) bool {
	return m.train[snippetFingerprint(code)]
}

// Predict classifies a snippet.
//
// The returned label is ClassificationVulnerable or ClassificationSafe, and
// the returned score is the probability the classifier gives to the
// vulnerable class. In the keyword mode the score is 1 or 0.
func (m *TFIDFModel) Predict(code string) (string, float64) {
	if m.mode == TFIDFModeKeyword {
		if keywordMatchTokens(Tokenize(code)) {
			return ClassificationVulnerable, 1
		}
		return ClassificationSafe, 0
	}
	x := m.vector(code)
	if len(x) == 0 {
		return ClassificationSafe, 0
	}
	// The score is the difference of the two log-likelihoods, plus the
	// difference of the log-priors.
	z := m.bias
	for _, f := range x {
		z += (m.vulnerableWeights[f.col] - m.safeWeights[f.col]) * f.weight
	}
	p := sigmoid(z)
	if p >= 0.5 {
		return ClassificationVulnerable, p
	}
	return ClassificationSafe, p
}

// ClassifyOutcome classifies a snippet into an Outcome.
//
// A snippet of the training split is answered with the classification
// "unknown" and with Completed false, so that an in-sample result can never
// be read as a held-out one.
func (m *TFIDFModel) ClassifyOutcome(code string) Outcome {
	if m.IsTrainingSnippet(code) {
		return Outcome{
			Classification: ClassificationUnknown,
			Completed:      false,
			Error: "bench: the snippet is part of the training split of the tfidf baseline; " +
				"it is not evaluated, because the model has seen it",
		}
	}
	label, _ := m.Predict(code)
	return Outcome{Classification: label, Completed: true}
}

// sigmoid is the logistic function.
func sigmoid(z float64) float64 {
	if z >= 0 {
		return 1 / (1 + math.Exp(-z))
	}
	e := math.Exp(z)
	return e / (1 + e)
}

// keywordMatchTokens reports whether a token stream trips the keyword
// heuristic.
//
// The rule is deliberately naive, and it is the control the classifier is
// measured against: a call to eval, exec, compile, pickle, tempfile.mktemp
// or a weak hash, or the co-occurrence of os and system, of subprocess and
// shell, of yaml and load, or of verify and false.
func keywordMatchTokens(tokens []string) bool {
	set := map[string]bool{}
	for _, tok := range tokens {
		set[tok] = true
	}
	for _, tok := range []string{"eval", "exec", "compile", "pickle", "mktemp", "md5", "sha1"} {
		if set[tok] {
			return true
		}
	}
	for _, pair := range [][2]string{
		{"os", "system"},
		{"subprocess", "shell"},
		{"yaml", "load"},
		{"verify", "false"},
	} {
		if set[pair[0]] && set[pair[1]] {
			return true
		}
	}
	return false
}

// tfidfModel holds the trained model the "tfidf" baseline uses.
//
// The benchmark harness has no way of passing a trained model through
// Options without touching the run path, so the model is installed once and
// read by the baseline.
var tfidfModel atomic.Pointer[TFIDFModel]

// SetTFIDFModel installs the model the "tfidf" baseline uses.
//
// A nil model removes the installed model.
func SetTFIDFModel(m *TFIDFModel) {
	tfidfModel.Store(m)
}

// CurrentTFIDFModel returns the installed model, or nil when none was
// installed.
func CurrentTFIDFModel() *TFIDFModel {
	return tfidfModel.Load()
}

// TrainTFIDFBaseline reads the corpus, splits it, and trains the baseline on
// the training split.
//
// The trained model is installed as the model of the "tfidf" baseline, so
// that a run with Options.Baseline set to "tfidf" uses it.
func TrainTFIDFBaseline(input string, opts TFIDFOptions) (*TFIDFModel, *Split, error) {
	table, err := ReadTable(input)
	if err != nil {
		return nil, nil, err
	}
	schema, err := DetectSchema(table.Header, "", "", "", "")
	if err != nil {
		return nil, nil, err
	}
	if schema.Mode != ModePaired {
		return nil, nil, fmt.Errorf(
			"bench: the tfidf baseline needs a paired corpus, got %q", schema.Mode)
	}
	split, err := SplitRows(table, opts.Split)
	if err != nil {
		return nil, nil, err
	}
	docs := documentsOf(table, schema, split.Train)
	model := TrainTFIDF(docs, opts)
	model.split = split
	SetTFIDFModel(model)
	return model, split, nil
}

// documentsOf returns the labelled documents of the given rows.
func documentsOf(table *Table, schema InputSchema, rows []int) []Document {
	docs := make([]Document, 0, len(rows)*2)
	for _, row := range rows {
		docs = append(docs,
			Document{Code: table.Cell(row, schema.BeforeCol), Label: ClassificationVulnerable},
			Document{Code: table.Cell(row, schema.AfterCol), Label: ClassificationSafe},
		)
	}
	return docs
}

// TFIDFEvaluation is the measured performance of the baseline on a split.
//
// The numbers are measured on the held-out split only. The accuracy on the
// training split is reported next to them, because the gap between the two
// is the size of the overfitting.
type TFIDFEvaluation struct {
	// Mode is the mode the model was trained in.
	Mode string
	// Seed is the seed of the split.
	Seed int64
	// TestFraction is the share of the groups that went to the held-out
	// split.
	TestFraction float64
	// GroupColumn is the column the rows were grouped by.
	GroupColumn string
	// SharedSnippets is the number of snippets that appear in both
	// splits.
	SharedSnippets int
	// TrainRows is the number of rows of the training split.
	TrainRows int
	// TestRows is the number of rows of the held-out split.
	TestRows int
	// TrainSnippets is the number of snippets of the training split.
	TrainSnippets int
	// TestSnippets is the number of snippets of the held-out split.
	TestSnippets int
	// TP is the number of vulnerable snippets of the held-out split that
	// were classified vulnerable.
	TP int
	// FP is the number of fixed snippets of the held-out split that were
	// classified vulnerable.
	FP int
	// FN is the number of vulnerable snippets of the held-out split that
	// were classified safe.
	FN int
	// TN is the number of fixed snippets of the held-out split that were
	// classified safe.
	TN int
	// CorrectBefore is the number of vulnerable snippets of the held-out
	// split that were classified vulnerable.
	CorrectBefore int
	// CorrectAfter is the number of fixed snippets of the held-out split
	// that were classified safe.
	CorrectAfter int
	// Accuracy is the share of the held-out snippets that were classified
	// correctly.
	Accuracy float64
	// Precision is the share of the reported findings that were correct.
	Precision float64
	// Recall is the share of the vulnerable snippets that were found.
	Recall float64
	// F1 is the harmonic mean of the precision and the recall.
	F1 float64
	// TrainAccuracy is the accuracy on the training split.
	TrainAccuracy float64
}

// EvaluateTFIDF measures the trained baseline on the held-out split.
//
// The evaluation never touches the training split: the numbers it returns
// are what a classifier that has never seen the snippet would score.
func EvaluateTFIDF(table *Table, schema InputSchema, model *TFIDFModel, split *Split) TFIDFEvaluation {
	ev := TFIDFEvaluation{
		Mode:           model.Mode(),
		Seed:           split.Seed,
		TestFraction:   split.TestFraction,
		GroupColumn:    split.GroupColumn,
		SharedSnippets: split.SharedSnippets,
		TrainRows:      len(split.Train),
		TestRows:       len(split.Test),
	}
	if model.split != nil {
		ev.Seed = model.split.Seed
	}

	score := func(rows []int) (tp, fp, fn, tn int) {
		for _, row := range rows {
			before := table.Cell(row, schema.BeforeCol)
			after := table.Cell(row, schema.AfterCol)

			label, _ := model.Predict(before)
			switch label {
			case ClassificationVulnerable:
				tp++
			default:
				fn++
			}
			label, _ = model.Predict(after)
			switch label {
			case ClassificationVulnerable:
				fp++
			default:
				tn++
			}
		}
		return tp, fp, fn, tn
	}

	ev.TP, ev.FP, ev.FN, ev.TN = score(split.Test)
	ev.TestSnippets = len(split.Test) * 2
	ev.CorrectBefore = ev.TP
	ev.CorrectAfter = ev.TN
	if ev.TestSnippets > 0 {
		ev.Accuracy = float64(ev.CorrectBefore+ev.CorrectAfter) / float64(ev.TestSnippets)
	}
	counts := TFIDFCounts{TP: ev.TP, FP: ev.FP, FN: ev.FN, TN: ev.TN}
	ev.Precision = counts.Precision()
	ev.Recall = counts.Recall()
	ev.F1 = counts.F1()

	tp, _, _, tn := score(split.Train)
	ev.TrainSnippets = len(split.Train) * 2
	if ev.TrainSnippets > 0 {
		ev.TrainAccuracy = float64(tp+tn) / float64(ev.TrainSnippets)
	}
	return ev
}

// TFIDFCounts is the confusion matrix of a binary classification.
type TFIDFCounts struct {
	// TP is the number of true positives.
	TP int
	// FP is the number of false positives.
	FP int
	// FN is the number of false negatives.
	FN int
	// TN is the number of true negatives.
	TN int
}

// Precision returns the share of the reported findings that are correct.
func (c TFIDFCounts) Precision() float64 {
	if c.TP+c.FP == 0 {
		return 0
	}
	return float64(c.TP) / float64(c.TP+c.FP)
}

// Recall returns the share of the real positives that were found.
func (c TFIDFCounts) Recall() float64 {
	if c.TP+c.FN == 0 {
		return 0
	}
	return float64(c.TP) / float64(c.TP+c.FN)
}

// F1 returns the harmonic mean of the precision and the recall.
func (c TFIDFCounts) F1() float64 {
	p, r := c.Precision(), c.Recall()
	if p+r == 0 {
		return 0
	}
	return 2 * p * r / (p + r)
}
