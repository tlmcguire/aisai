

import math
import re
from collections import defaultdict

from nltk.probability import FreqDist
from nltk.tokenize.api import TokenizerI

_ORTHO_BEG_UC = 1 << 1

_ORTHO_MID_UC = 1 << 2

_ORTHO_UNK_UC = 1 << 3

_ORTHO_BEG_LC = 1 << 4

_ORTHO_MID_LC = 1 << 5

_ORTHO_UNK_LC = 1 << 6

_ORTHO_UC = _ORTHO_BEG_UC + _ORTHO_MID_UC + _ORTHO_UNK_UC

_ORTHO_LC = _ORTHO_BEG_LC + _ORTHO_MID_LC + _ORTHO_UNK_LC

_ORTHO_MAP = {
    ("initial", "upper"): _ORTHO_BEG_UC,
    ("internal", "upper"): _ORTHO_MID_UC,
    ("unknown", "upper"): _ORTHO_UNK_UC,
    ("initial", "lower"): _ORTHO_BEG_LC,
    ("internal", "lower"): _ORTHO_MID_LC,
    ("unknown", "lower"): _ORTHO_UNK_LC,
}

REASON_DEFAULT_DECISION = "default decision"
REASON_KNOWN_COLLOCATION = "known collocation (both words)"
REASON_ABBR_WITH_ORTHOGRAPHIC_HEURISTIC = "abbreviation + orthographic heuristic"
REASON_ABBR_WITH_SENTENCE_STARTER = "abbreviation + frequent sentence starter"
REASON_INITIAL_WITH_ORTHOGRAPHIC_HEURISTIC = "initial + orthographic heuristic"
REASON_NUMBER_WITH_ORTHOGRAPHIC_HEURISTIC = "initial + orthographic heuristic"
REASON_INITIAL_WITH_SPECIAL_ORTHOGRAPHIC_HEURISTIC = (
    "initial + special orthographic heuristic"
)

class PunktLanguageVars:


    __slots__ = ("_re_period_context", "_re_word_tokenizer")

    def __getstate__(self):
        return 1

    def __setstate__(self, state):
        return 1

    sent_end_chars = (".", "?", "!")

    @property
    def _re_sent_end_chars(self):
        return "[%s]" % re.escape("".join(self.sent_end_chars))

    internal_punctuation = ",:;"

    re_boundary_realignment = re.compile(r'["\')\]}]+?(?:\s+|(?=--)|$)', re.MULTILINE)

    _re_word_start = r"[^\(\"\`{\[:;&\#\*@\)}\]\-,]"

    @property
    def _re_non_word_chars(self):
        return r"(?:[)\";}\]\*:@\'\({\[%s])" % re.escape(
            "".join(set(self.sent_end_chars) - {"."})
        )

    _re_multi_char_punct = r"(?:\-{2,}|\.{2,}|(?:\.\s){2,}\.)"

    _word_tokenize_fmt = r"""(
        %(MultiChar)s
        |
        (?=%(WordStart)s)\S+?  # Accept word characters until end is found
        (?= # Sequences marking a word's end
            \s|                                 # White-space
            $|                                  # End-of-string
            %(NonWord)s|%(MultiChar)s|          # Punctuation
            ,(?=$|\s|%(NonWord)s|%(MultiChar)s) # Comma if at end of word
        )
        |
        \S
    )"""

    def _word_tokenizer_re(self):

        try:
            return self._re_word_tokenizer
        except AttributeError:
            self._re_word_tokenizer = re.compile(
                self._word_tokenize_fmt
                % {
                    "NonWord": self._re_non_word_chars,
                    "MultiChar": self._re_multi_char_punct,
                    "WordStart": self._re_word_start,
                },
                re.UNICODE | re.VERBOSE,
            )
            return self._re_word_tokenizer

    def word_tokenize(self, s):

        return self._word_tokenizer_re().findall(s)

    _period_context_fmt = r"""
        %(SentEndChars)s             # a potential sentence ending
        (?=(?P<after_tok>
            %(NonWord)s              # either other punctuation
            |
            \s+(?P<next_tok>\S+)     # or whitespace and some other token
        ))"""

    def period_context_re(self):

        try:
            return self._re_period_context
        except:
            self._re_period_context = re.compile(
                self._period_context_fmt
                % {
                    "NonWord": self._re_non_word_chars,
                    "SentEndChars": self._re_sent_end_chars,
                },
                re.UNICODE | re.VERBOSE,
            )
            return self._re_period_context

_re_non_punct = re.compile(r"[^\W\d]", re.UNICODE)

def _pair_iter(iterator):

    iterator = iter(iterator)
    try:
        prev = next(iterator)
    except StopIteration:
        return
    for el in iterator:
        yield (prev, el)
        prev = el
    yield (prev, None)

class PunktParameters:


    def __init__(self):
        self.abbrev_types = set()

        self.collocations = set()

        self.sent_starters = set()

        self.ortho_context = defaultdict(int)

    def clear_abbrevs(self):
        self.abbrev_types = set()

    def clear_collocations(self):
        self.collocations = set()

    def clear_sent_starters(self):
        self.sent_starters = set()

    def clear_ortho_context(self):
        self.ortho_context = defaultdict(int)

    def add_ortho_context(self, typ, flag):
        self.ortho_context[typ] |= flag

    def _debug_ortho_context(self, typ):
        context = self.ortho_context[typ]
        if context & _ORTHO_BEG_UC:
            yield "BEG-UC"
        if context & _ORTHO_MID_UC:
            yield "MID-UC"
        if context & _ORTHO_UNK_UC:
            yield "UNK-UC"
        if context & _ORTHO_BEG_LC:
            yield "BEG-LC"
        if context & _ORTHO_MID_LC:
            yield "MID-LC"
        if context & _ORTHO_UNK_LC:
            yield "UNK-LC"

class PunktToken:


    _properties = ["parastart", "linestart", "sentbreak", "abbr", "ellipsis"]
    __slots__ = ["tok", "type", "period_final"] + _properties

    def __init__(self, tok, **params):
        self.tok = tok
        self.type = self._get_type(tok)
        self.period_final = tok.endswith(".")

        for prop in self._properties:
            setattr(self, prop, None)
        for k in params:
            setattr(self, k, params[k])

    _RE_ELLIPSIS = re.compile(r"\.\.+$")
    _RE_NUMERIC = re.compile(r"^-?[\.,]?\d[\d,\.-]*\.?$")
    _RE_INITIAL = re.compile(r"[^\W\d]\.$", re.UNICODE)
    _RE_ALPHA = re.compile(r"[^\W\d]+$", re.UNICODE)

    def _get_type(self, tok):

        return self._RE_NUMERIC.sub("##number##", tok.lower())

    @property
    def type_no_period(self):

        if len(self.type) > 1 and self.type[-1] == ".":
            return self.type[:-1]
        return self.type

    @property
    def type_no_sentperiod(self):

        if self.sentbreak:
            return self.type_no_period
        return self.type

    @property
    def first_upper(self):

        return self.tok[0].isupper()

    @property
    def first_lower(self):

        return self.tok[0].islower()

    @property
    def first_case(self):
        if self.first_lower:
            return "lower"
        if self.first_upper:
            return "upper"
        return "none"

    @property
    def is_ellipsis(self):

        return self._RE_ELLIPSIS.match(self.tok)

    @property
    def is_number(self):

        return self.type.startswith("##number##")

    @property
    def is_initial(self):

        return self._RE_INITIAL.match(self.tok)

    @property
    def is_alpha(self):

        return self._RE_ALPHA.match(self.tok)

    @property
    def is_non_punct(self):

        return _re_non_punct.search(self.type)

    def __repr__(self):

        typestr = " type=%s," % repr(self.type) if self.type != self.tok else ""

        propvals = ", ".join(
            f"{p}={repr(getattr(self, p))}"
            for p in self._properties
            if getattr(self, p)
        )

        return "{}({},{} {})".format(
            self.__class__.__name__,
            repr(self.tok),
            typestr,
            propvals,
        )

    def __str__(self):

        res = self.tok
        if self.abbr:
            res += "<A>"
        if self.ellipsis:
            res += "<E>"
        if self.sentbreak:
            res += "<S>"
        return res

class PunktBaseClass:


    def __init__(self, lang_vars=None, token_cls=PunktToken, params=None):
        if lang_vars is None:
            lang_vars = PunktLanguageVars()
        if params is None:
            params = PunktParameters()
        self._params = params
        self._lang_vars = lang_vars
        self._Token = token_cls

    def _tokenize_words(self, plaintext):

        parastart = False
        for line in plaintext.split("\n"):
            if line.strip():
                line_toks = iter(self._lang_vars.word_tokenize(line))

                try:
                    tok = next(line_toks)
                except StopIteration:
                    continue

                yield self._Token(tok, parastart=parastart, linestart=True)
                parastart = False

                for tok in line_toks:
                    yield self._Token(tok)
            else:
                parastart = True

    def _annotate_first_pass(self, tokens):

        for aug_tok in tokens:
            self._first_pass_annotation(aug_tok)
            yield aug_tok

    def _first_pass_annotation(self, aug_tok):


        tok = aug_tok.tok

        if tok in self._lang_vars.sent_end_chars:
            aug_tok.sentbreak = True
        elif aug_tok.is_ellipsis:
            aug_tok.ellipsis = True
        elif aug_tok.period_final and not tok.endswith(".."):
            if (
                tok[:-1].lower() in self._params.abbrev_types
                or tok[:-1].lower().split("-")[-1] in self._params.abbrev_types
            ):

                aug_tok.abbr = True
            else:
                aug_tok.sentbreak = True

        return

class PunktTrainer(PunktBaseClass):


    def __init__(
        self, train_text=None, verbose=False, lang_vars=None, token_cls=PunktToken
    ):

        PunktBaseClass.__init__(self, lang_vars=lang_vars, token_cls=token_cls)

        self._type_fdist = FreqDist()

        self._num_period_toks = 0

        self._collocation_fdist = FreqDist()

        self._sent_starter_fdist = FreqDist()

        self._sentbreak_count = 0

        self._finalized = True

        if train_text:
            self.train(train_text, verbose, finalize=True)

    def get_params(self):

        if not self._finalized:
            self.finalize_training()
        return self._params

    ABBREV = 0.3

    IGNORE_ABBREV_PENALTY = False

    ABBREV_BACKOFF = 5

    COLLOCATION = 7.88

    SENT_STARTER = 30

    INCLUDE_ALL_COLLOCS = False

    INCLUDE_ABBREV_COLLOCS = False

    MIN_COLLOC_FREQ = 1

    def train(self, text, verbose=False, finalize=True):

        self._train_tokens(self._tokenize_words(text), verbose)
        if finalize:
            self.finalize_training(verbose)

    def train_tokens(self, tokens, verbose=False, finalize=True):

        self._train_tokens((self._Token(t) for t in tokens), verbose)
        if finalize:
            self.finalize_training(verbose)

    def _train_tokens(self, tokens, verbose):
        self._finalized = False

        tokens = list(tokens)

        for aug_tok in tokens:
            self._type_fdist[aug_tok.type] += 1
            if aug_tok.period_final:
                self._num_period_toks += 1

        unique_types = self._unique_types(tokens)
        for abbr, score, is_add in self._reclassify_abbrev_types(unique_types):
            if score >= self.ABBREV:
                if is_add:
                    self._params.abbrev_types.add(abbr)
                    if verbose:
                        print(f"  Abbreviation: [{score:6.4f}] {abbr}")
            else:
                if not is_add:
                    self._params.abbrev_types.remove(abbr)
                    if verbose:
                        print(f"  Removed abbreviation: [{score:6.4f}] {abbr}")

        tokens = list(self._annotate_first_pass(tokens))

        self._get_orthography_data(tokens)

        self._sentbreak_count += self._get_sentbreak_count(tokens)

        for aug_tok1, aug_tok2 in _pair_iter(tokens):
            if not aug_tok1.period_final or not aug_tok2:
                continue

            if self._is_rare_abbrev_type(aug_tok1, aug_tok2):
                self._params.abbrev_types.add(aug_tok1.type_no_period)
                if verbose:
                    print("  Rare Abbrev: %s" % aug_tok1.type)

            if self._is_potential_sent_starter(aug_tok2, aug_tok1):
                self._sent_starter_fdist[aug_tok2.type] += 1

            if self._is_potential_collocation(aug_tok1, aug_tok2):
                self._collocation_fdist[
                    (aug_tok1.type_no_period, aug_tok2.type_no_sentperiod)
                ] += 1

    def _unique_types(self, tokens):
        return {aug_tok.type for aug_tok in tokens}

    def finalize_training(self, verbose=False):

        self._params.clear_sent_starters()
        for typ, log_likelihood in self._find_sent_starters():
            self._params.sent_starters.add(typ)
            if verbose:
                print(f"  Sent Starter: [{log_likelihood:6.4f}] {typ!r}")

        self._params.clear_collocations()
        for (typ1, typ2), log_likelihood in self._find_collocations():
            self._params.collocations.add((typ1, typ2))
            if verbose:
                print(f"  Collocation: [{log_likelihood:6.4f}] {typ1!r}+{typ2!r}")

        self._finalized = True

    def freq_threshold(
        self, ortho_thresh=2, type_thresh=2, colloc_thres=2, sentstart_thresh=2
    ):

        if ortho_thresh > 1:
            old_oc = self._params.ortho_context
            self._params.clear_ortho_context()
            for tok in self._type_fdist:
                count = self._type_fdist[tok]
                if count >= ortho_thresh:
                    self._params.ortho_context[tok] = old_oc[tok]

        self._type_fdist = self._freq_threshold(self._type_fdist, type_thresh)
        self._collocation_fdist = self._freq_threshold(
            self._collocation_fdist, colloc_thres
        )
        self._sent_starter_fdist = self._freq_threshold(
            self._sent_starter_fdist, sentstart_thresh
        )

    def _freq_threshold(self, fdist, threshold):

        res = FreqDist()
        num_removed = 0
        for tok in fdist:
            count = fdist[tok]
            if count < threshold:
                num_removed += 1
            else:
                res[tok] += count
        res[None] += num_removed
        return res

    def _get_orthography_data(self, tokens):

        context = "internal"
        tokens = list(tokens)

        for aug_tok in tokens:
            if aug_tok.parastart and context != "unknown":
                context = "initial"

            if aug_tok.linestart and context == "internal":
                context = "unknown"

            typ = aug_tok.type_no_sentperiod

            flag = _ORTHO_MAP.get((context, aug_tok.first_case), 0)
            if flag:
                self._params.add_ortho_context(typ, flag)

            if aug_tok.sentbreak:
                if not (aug_tok.is_number or aug_tok.is_initial):
                    context = "initial"
                else:
                    context = "unknown"
            elif aug_tok.ellipsis or aug_tok.abbr:
                context = "unknown"
            else:
                context = "internal"

    def _reclassify_abbrev_types(self, types):


        for typ in types:
            if not _re_non_punct.search(typ) or typ == "##number##":
                continue

            if typ.endswith("."):
                if typ in self._params.abbrev_types:
                    continue
                typ = typ[:-1]
                is_add = True
            else:
                if typ not in self._params.abbrev_types:
                    continue
                is_add = False

            num_periods = typ.count(".") + 1
            num_nonperiods = len(typ) - num_periods + 1

            count_with_period = self._type_fdist[typ + "."]
            count_without_period = self._type_fdist[typ]
            log_likelihood = self._dunning_log_likelihood(
                count_with_period + count_without_period,
                self._num_period_toks,
                count_with_period,
                self._type_fdist.N(),
            )

            f_length = math.exp(-num_nonperiods)
            f_periods = num_periods
            f_penalty = int(self.IGNORE_ABBREV_PENALTY) or math.pow(
                num_nonperiods, -count_without_period
            )
            score = log_likelihood * f_length * f_periods * f_penalty

            yield typ, score, is_add

    def find_abbrev_types(self):

        self._params.clear_abbrevs()
        tokens = (typ for typ in self._type_fdist if typ and typ.endswith("."))
        for abbr, score, _is_add in self._reclassify_abbrev_types(tokens):
            if score >= self.ABBREV:
                self._params.abbrev_types.add(abbr)

    def _is_rare_abbrev_type(self, cur_tok, next_tok):

        if cur_tok.abbr or not cur_tok.sentbreak:
            return False

        typ = cur_tok.type_no_sentperiod

        count = self._type_fdist[typ] + self._type_fdist[typ[:-1]]
        if typ in self._params.abbrev_types or count >= self.ABBREV_BACKOFF:
            return False

        if next_tok.tok[:1] in self._lang_vars.internal_punctuation:
            return True

        if next_tok.first_lower:
            typ2 = next_tok.type_no_sentperiod
            typ2ortho_context = self._params.ortho_context[typ2]
            if (typ2ortho_context & _ORTHO_BEG_UC) and not (
                typ2ortho_context & _ORTHO_MID_UC
            ):
                return True

    @staticmethod
    def _dunning_log_likelihood(count_a, count_b, count_ab, N):

        p1 = count_b / N
        p2 = 0.99

        null_hypo = count_ab * math.log(p1) + (count_a - count_ab) * math.log(1.0 - p1)
        alt_hypo = count_ab * math.log(p2) + (count_a - count_ab) * math.log(1.0 - p2)

        likelihood = null_hypo - alt_hypo

        return -2.0 * likelihood

    @staticmethod
    def _col_log_likelihood(count_a, count_b, count_ab, N):

        p = count_b / N
        p1 = count_ab / count_a
        try:
            p2 = (count_b - count_ab) / (N - count_a)
        except ZeroDivisionError:
            p2 = 1

        try:
            summand1 = count_ab * math.log(p) + (count_a - count_ab) * math.log(1.0 - p)
        except ValueError:
            summand1 = 0

        try:
            summand2 = (count_b - count_ab) * math.log(p) + (
                N - count_a - count_b + count_ab
            ) * math.log(1.0 - p)
        except ValueError:
            summand2 = 0

        if count_a == count_ab or p1 <= 0 or p1 >= 1:
            summand3 = 0
        else:
            summand3 = count_ab * math.log(p1) + (count_a - count_ab) * math.log(
                1.0 - p1
            )

        if count_b == count_ab or p2 <= 0 or p2 >= 1:
            summand4 = 0
        else:
            summand4 = (count_b - count_ab) * math.log(p2) + (
                N - count_a - count_b + count_ab
            ) * math.log(1.0 - p2)

        likelihood = summand1 + summand2 - summand3 - summand4

        return -2.0 * likelihood

    def _is_potential_collocation(self, aug_tok1, aug_tok2):

        return (
            (
                self.INCLUDE_ALL_COLLOCS
                or (self.INCLUDE_ABBREV_COLLOCS and aug_tok1.abbr)
                or (aug_tok1.sentbreak and (aug_tok1.is_number or aug_tok1.is_initial))
            )
            and aug_tok1.is_non_punct
            and aug_tok2.is_non_punct
        )

    def _find_collocations(self):

        for types in self._collocation_fdist:
            try:
                typ1, typ2 = types
            except TypeError:
                continue
            if typ2 in self._params.sent_starters:
                continue

            col_count = self._collocation_fdist[types]
            typ1_count = self._type_fdist[typ1] + self._type_fdist[typ1 + "."]
            typ2_count = self._type_fdist[typ2] + self._type_fdist[typ2 + "."]
            if (
                typ1_count > 1
                and typ2_count > 1
                and self.MIN_COLLOC_FREQ < col_count <= min(typ1_count, typ2_count)
            ):

                log_likelihood = self._col_log_likelihood(
                    typ1_count, typ2_count, col_count, self._type_fdist.N()
                )
                if log_likelihood >= self.COLLOCATION and (
                    self._type_fdist.N() / typ1_count > typ2_count / col_count
                ):
                    yield (typ1, typ2), log_likelihood

    def _is_potential_sent_starter(self, cur_tok, prev_tok):

        return (
            prev_tok.sentbreak
            and not (prev_tok.is_number or prev_tok.is_initial)
            and cur_tok.is_alpha
        )

    def _find_sent_starters(self):

        for typ in self._sent_starter_fdist:
            if not typ:
                continue

            typ_at_break_count = self._sent_starter_fdist[typ]
            typ_count = self._type_fdist[typ] + self._type_fdist[typ + "."]
            if typ_count < typ_at_break_count:
                continue

            log_likelihood = self._col_log_likelihood(
                self._sentbreak_count,
                typ_count,
                typ_at_break_count,
                self._type_fdist.N(),
            )

            if (
                log_likelihood >= self.SENT_STARTER
                and self._type_fdist.N() / self._sentbreak_count
                > typ_count / typ_at_break_count
            ):
                yield typ, log_likelihood

    def _get_sentbreak_count(self, tokens):

        return sum(1 for aug_tok in tokens if aug_tok.sentbreak)

class PunktSentenceTokenizer(PunktBaseClass, TokenizerI):


    def __init__(
        self, train_text=None, verbose=False, lang_vars=None, token_cls=PunktToken
    ):

        PunktBaseClass.__init__(self, lang_vars=lang_vars, token_cls=token_cls)

        if train_text:
            self._params = self.train(train_text, verbose)

    def train(self, train_text, verbose=False):

        if not isinstance(train_text, str):
            return train_text
        return PunktTrainer(
            train_text, lang_vars=self._lang_vars, token_cls=self._Token
        ).get_params()

    def tokenize(self, text, realign_boundaries=True):

        return list(self.sentences_from_text(text, realign_boundaries))

    def debug_decisions(self, text):


        for match, decision_text in self._match_potential_end_contexts(text):
            tokens = self._tokenize_words(decision_text)
            tokens = list(self._annotate_first_pass(tokens))
            while tokens and not tokens[0].tok.endswith(self._lang_vars.sent_end_chars):
                tokens.pop(0)
            yield {
                "period_index": match.end() - 1,
                "text": decision_text,
                "type1": tokens[0].type,
                "type2": tokens[1].type,
                "type1_in_abbrs": bool(tokens[0].abbr),
                "type1_is_initial": bool(tokens[0].is_initial),
                "type2_is_sent_starter": tokens[1].type_no_sentperiod
                in self._params.sent_starters,
                "type2_ortho_heuristic": self._ortho_heuristic(tokens[1]),
                "type2_ortho_contexts": set(
                    self._params._debug_ortho_context(tokens[1].type_no_sentperiod)
                ),
                "collocation": (
                    tokens[0].type_no_sentperiod,
                    tokens[1].type_no_sentperiod,
                )
                in self._params.collocations,
                "reason": self._second_pass_annotation(tokens[0], tokens[1])
                or REASON_DEFAULT_DECISION,
                "break_decision": tokens[0].sentbreak,
            }

    def span_tokenize(self, text, realign_boundaries=True):

        slices = self._slices_from_text(text)
        if realign_boundaries:
            slices = self._realign_boundaries(text, slices)
        for sentence in slices:
            yield (sentence.start, sentence.stop)

    def sentences_from_text(self, text, realign_boundaries=True):

        return [text[s:e] for s, e in self.span_tokenize(text, realign_boundaries)]

    def _match_potential_end_contexts(self, text):

        before_words = {}
        matches = []
        for match in reversed(list(self._lang_vars.period_context_re().finditer(text))):
            if matches and match.end() > before_start:
                continue
            split = text[: match.start()].rsplit(maxsplit=1)
            before_start = len(split[0]) if len(split) == 2 else 0
            before_words[match] = split[-1]
            matches.append(match)

        return [
            (
                match,
                before_words[match] + match.group() + match.group("after_tok"),
            )
            for match in matches[::-1]
        ]

    def _slices_from_text(self, text):
        last_break = 0
        for match, context in self._match_potential_end_contexts(text):
            if self.text_contains_sentbreak(context):
                yield slice(last_break, match.end())
                if match.group("next_tok"):
                    last_break = match.start("next_tok")
                else:
                    last_break = match.end()
        yield slice(last_break, len(text.rstrip()))

    def _realign_boundaries(self, text, slices):

        realign = 0
        for sentence1, sentence2 in _pair_iter(slices):
            sentence1 = slice(sentence1.start + realign, sentence1.stop)
            if not sentence2:
                if text[sentence1]:
                    yield sentence1
                continue

            m = self._lang_vars.re_boundary_realignment.match(text[sentence2])
            if m:
                yield slice(sentence1.start, sentence2.start + len(m.group(0).rstrip()))
                realign = m.end()
            else:
                realign = 0
                if text[sentence1]:
                    yield sentence1

    def text_contains_sentbreak(self, text):

        found = False
        for tok in self._annotate_tokens(self._tokenize_words(text)):
            if found:
                return True
            if tok.sentbreak:
                found = True
        return False

    def sentences_from_text_legacy(self, text):

        tokens = self._annotate_tokens(self._tokenize_words(text))
        return self._build_sentence_list(text, tokens)

    def sentences_from_tokens(self, tokens):

        tokens = iter(self._annotate_tokens(self._Token(t) for t in tokens))
        sentence = []
        for aug_tok in tokens:
            sentence.append(aug_tok.tok)
            if aug_tok.sentbreak:
                yield sentence
                sentence = []
        if sentence:
            yield sentence

    def _annotate_tokens(self, tokens):

        tokens = self._annotate_first_pass(tokens)

        tokens = self._annotate_second_pass(tokens)

        return tokens

    def _build_sentence_list(self, text, tokens):


        pos = 0

        white_space_regexp = re.compile(r"\s*")

        sentence = ""
        for aug_tok in tokens:
            tok = aug_tok.tok

            white_space = white_space_regexp.match(text, pos).group()
            pos += len(white_space)

            if text[pos : pos + len(tok)] != tok:
                pat = r"\s*".join(re.escape(c) for c in tok)
                m = re.compile(pat).match(text, pos)
                if m:
                    tok = m.group()

            assert text[pos : pos + len(tok)] == tok
            pos += len(tok)

            if sentence:
                sentence += white_space
            sentence += tok

            if aug_tok.sentbreak:
                yield sentence
                sentence = ""

        if sentence:
            yield sentence

    def dump(self, tokens):
        print("writing to /tmp/punkt.new...")
        with open("/tmp/punkt.new", "w") as outfile:
            for aug_tok in tokens:
                if aug_tok.parastart:
                    outfile.write("\n\n")
                elif aug_tok.linestart:
                    outfile.write("\n")
                else:
                    outfile.write(" ")

                outfile.write(str(aug_tok))

    PUNCTUATION = tuple(";:,.!?")

    def _annotate_second_pass(self, tokens):

        for token1, token2 in _pair_iter(tokens):
            self._second_pass_annotation(token1, token2)
            yield token1

    def _second_pass_annotation(self, aug_tok1, aug_tok2):

        if not aug_tok2:
            return

        if not aug_tok1.period_final:
            return
        typ = aug_tok1.type_no_period
        next_typ = aug_tok2.type_no_sentperiod
        tok_is_initial = aug_tok1.is_initial

        if (typ, next_typ) in self._params.collocations:
            aug_tok1.sentbreak = False
            aug_tok1.abbr = True
            return REASON_KNOWN_COLLOCATION

        if (aug_tok1.abbr or aug_tok1.ellipsis) and (not tok_is_initial):
            is_sent_starter = self._ortho_heuristic(aug_tok2)
            if is_sent_starter == True:
                aug_tok1.sentbreak = True
                return REASON_ABBR_WITH_ORTHOGRAPHIC_HEURISTIC

            if aug_tok2.first_upper and next_typ in self._params.sent_starters:
                aug_tok1.sentbreak = True
                return REASON_ABBR_WITH_SENTENCE_STARTER

        if tok_is_initial or typ == "##number##":

            is_sent_starter = self._ortho_heuristic(aug_tok2)

            if is_sent_starter == False:
                aug_tok1.sentbreak = False
                aug_tok1.abbr = True
                if tok_is_initial:
                    return REASON_INITIAL_WITH_ORTHOGRAPHIC_HEURISTIC
                return REASON_NUMBER_WITH_ORTHOGRAPHIC_HEURISTIC

            if (
                is_sent_starter == "unknown"
                and tok_is_initial
                and aug_tok2.first_upper
                and not (self._params.ortho_context[next_typ] & _ORTHO_LC)
            ):
                aug_tok1.sentbreak = False
                aug_tok1.abbr = True
                return REASON_INITIAL_WITH_SPECIAL_ORTHOGRAPHIC_HEURISTIC

        return

    def _ortho_heuristic(self, aug_tok):

        if aug_tok.tok in self.PUNCTUATION:
            return False

        ortho_context = self._params.ortho_context[aug_tok.type_no_sentperiod]

        if (
            aug_tok.first_upper
            and (ortho_context & _ORTHO_LC)
            and not (ortho_context & _ORTHO_MID_UC)
        ):
            return True

        if aug_tok.first_lower and (
            (ortho_context & _ORTHO_UC) or not (ortho_context & _ORTHO_BEG_LC)
        ):
            return False

        return "unknown"

DEBUG_DECISION_FMT = """Text: {text!r} (at offset {period_index})
Sentence break? {break_decision} ({reason})
Collocation? {collocation}
{type1!r}:
    known abbreviation: {type1_in_abbrs}
    is initial: {type1_is_initial}
{type2!r}:
    known sentence starter: {type2_is_sent_starter}
    orthographic heuristic suggests is a sentence starter? {type2_ortho_heuristic}
    orthographic contexts in training: {type2_ortho_contexts}
"""

def format_debug_decision(d):
    return DEBUG_DECISION_FMT.format(**d)

def demo(text, tok_cls=PunktSentenceTokenizer, train_cls=PunktTrainer):

    cleanup = (
        lambda s: re.compile(r"(?:\r|^\s+)", re.MULTILINE).sub("", s).replace("\n", " ")
    )
    trainer = train_cls()
    trainer.INCLUDE_ALL_COLLOCS = True
    trainer.train(text)
    sbd = tok_cls(trainer.get_params())
    for sentence in sbd.sentences_from_text(text):
        print(cleanup(sentence))
