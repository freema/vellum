package vault

import (
	"fmt"
	"math"
	"strings"
	"unicode"
)

// BM25F ranking (NSO-577).
//
// Every query term is counted per field of a note (title, path, headings,
// tags, body). The field counts are weighted, normalised by the field's
// length against its mean over the candidates, and summed:
//
//	tf' = Σ_field weight · tf_field / (1 − b + b · len_field / avglen_field)
//
// then saturated and weighted by how rare the term is among the candidates:
//
//	score = Σ_term IDF · tf' / (k1 + tf'),  IDF = ln(1 + (N − n + 0.5) / (n + 0.5))
//
// and divided by Σ_term IDF. tf'/(k1+tf') is below 1, so the relevance lies
// in 0..1 whatever the number of terms, and it is not divided by the best
// hit: a weak match stays weak even when it is the only one.
//
// There is no index. The scan already opens every candidate; phase 1 counts
// in how many candidates each term occurs and sums the field lengths (per
// worker, merged afterwards), phase 2 turns the counts into scores. The
// statistics are taken over the candidates left after the tag and dir
// filters, so notes the query does not concern do not move its scores.
// Nothing is cached beyond a few numbers per note (cachedNote.fieldLen,
// bodyOff, headings).

// Ranker names for VELLUM_RANKER while BM25F rolls out: both rankers can be
// compared over the same vault and golden set. RankerLegacy goes once BM25F
// is accepted.
const (
	RankerBM25F  = "bm25f"
	RankerLegacy = "legacy"
)

// SetRanker picks the ranking of text queries: RankerBM25F (the default)
// or RankerLegacy. Call it before the searcher serves queries.
func (s *ScanSearcher) SetRanker(name string) error {
	switch name {
	case "", RankerBM25F:
		s.ranker = RankerBM25F
	case RankerLegacy:
		s.ranker = RankerLegacy
	default:
		return fmt.Errorf("unknown ranker %q (want %s or %s)", name, RankerBM25F, RankerLegacy)
	}
	return nil
}

// Fields of a note, in the order of bm25Fields and cachedNote.fieldLen.
const (
	fieldTitle = iota
	fieldPath
	fieldHeadings
	fieldTags
	fieldBody
	numFields
)

// bm25Field is one field's weight and length normalisation (b: 0 = length
// does not matter, 1 = fully normalised).
type bm25Field struct{ weight, b float64 }

// bm25Fields are the field weights of NSO-577. A sweep on the golden set
// (k1 0.9 to 2, body b 0.5 to 1, other weights 1 to 8) found no better
// setting; see docs/search-quality.md.
var bm25Fields = [numFields]bm25Field{
	fieldTitle:    {weight: 5, b: 0.3},
	fieldPath:     {weight: 3, b: 0},
	fieldHeadings: {weight: 3, b: 0.5},
	fieldTags:     {weight: 3, b: 0},
	fieldBody:     {weight: 1, b: 0.75},
}

const (
	// bm25K1 is the term frequency saturation: the higher, the more a
	// repeated term keeps adding.
	bm25K1 = 1.2
	// midWordWeight is what an occurrence inside a word counts ("faktur"
	// in "nefakturovano") against one at a word start: matching is by
	// substring, as before, but a hit at a word start is the better one.
	midWordWeight = 0.5
	// typoWeight scales the contribution of a term that only matched
	// through the typo fallback. A rescue, not a match. NSO-578 measured a
	// weight by edit distance (0.45 · len / (len + distance)) instead: no
	// change on the golden set, so the flat weight stayed.
	typoWeight = 0.5
	// scoreDecimals is the precision Result.Score is rounded to; ranking
	// and MinScore work on the rounded value, so equal scores as shown
	// tie-break by modification time and path.
	scoreDecimals = 1000
)

// bm25Stats is one worker's share of the corpus statistics: per term the
// number of candidates it occurs in, and per field the summed word counts
// of all candidates.
type bm25Stats struct {
	df     []int
	lenSum [numFields]int64
	n      int // candidates seen
	// slab holds the per-field counts of this worker's matches, one
	// growing allocation instead of one per match; scored.tfOff points
	// into it.
	slab []float64
}

// fillFields derives the BM25F field data of a freshly cached note: the
// body offset after the frontmatter, the heading texts (outside fenced
// code), and the word count of every field.
func (c *cachedNote) fillFields(raw string) {
	bodyLine := 0
	if _, body := splitFrontmatter(raw); len(body) < len(raw) {
		bodyLine = strings.Count(raw[:len(raw)-len(body)], "\n")
	}
	bodyLine = min(bodyLine, len(c.folded))
	for _, l := range c.folded[:bodyLine] {
		c.bodyOff += len(l) + 1
	}
	c.bodyOff = min(c.bodyOff, len(c.all))

	pos := make(map[string]int, len(c.words))
	for i, w := range c.words {
		pos[w.s] = i
	}
	var headings []string
	headingWords, bodyWords := 0, 0
	inFence := false
	for i := bodyLine; i < len(c.lines); i++ {
		raw := c.lines[i]
		if fenceRe.MatchString(raw) {
			inFence = !inFence
		} else if !inFence && strings.HasPrefix(raw, "#") && headingRe.MatchString(strings.TrimRight(raw, "\r")) {
			h := strings.TrimSpace(strings.TrimLeft(c.folded[i], "#"))
			headings = append(headings, h)
			headingWords += countWords(h)
			continue
		}
		forEachWord(c.folded[i], func(w string) {
			bodyWords++
			if j, ok := pos[w]; ok {
				c.words[j].body++
			}
		})
	}
	c.headings = strings.Join(headings, "\n")
	// Counting a term over the unique words beats scanning the text when
	// the text repeats its words a lot (see bodyTermCount).
	c.countByWords = len(c.words) < (len(c.all)-c.bodyOff)/wordsPerScanByte

	tagWords := 0
	for _, t := range c.tags {
		tagWords += countWords(t)
	}
	c.fieldLen = [numFields]int32{
		fieldTitle:    int32(countWords(c.title)),
		fieldPath:     int32(countWords(c.path)),
		fieldHeadings: int32(headingWords),
		fieldTags:     int32(tagWords),
		fieldBody:     int32(bodyWords),
	}
}

// wordsPerScanByte is the break-even of bodyTermCount: scanning the text
// costs about one unit per this many bytes, checking a unique word about
// one unit.
const wordsPerScanByte = 64

// isWordRune reports whether r belongs to a word (as collectWords splits).
func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// forEachWord calls fn for every run of letters and digits in s.
func forEachWord(s string, fn func(string)) {
	start := -1
	for i, r := range s {
		if isWordRune(r) {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			fn(s[start:i])
			start = -1
		}
	}
	if start >= 0 {
		fn(s[start:])
	}
}

// countWords counts the runs of letters and digits in s.
func countWords(s string) int {
	n := 0
	forEachWord(s, func(string) { n++ })
	return n
}

// isWordTerm reports whether term is made of letters and digits only, so
// every occurrence of it lies inside one word.
func isWordTerm(term string) bool {
	for _, r := range term {
		if !isWordRune(r) {
			return false
		}
	}
	return term != ""
}

// termCount counts the non-overlapping occurrences of term in s, one at a
// word start as 1 and one inside a word as midWordWeight.
func termCount(s, term string) float64 {
	n := 0.0
	for i := 0; i <= len(s); {
		j := strings.Index(s[i:], term)
		if j < 0 {
			break
		}
		if wordBoundaryAt(s, i+j) {
			n++
		} else {
			n += midWordWeight
		}
		i += j + len(term)
	}
	return n
}

// bodyTermCount counts term in the body (after the frontmatter, headings
// left out) like termCount would. A note that repeats its words a lot is
// counted over its unique words and their body counts instead of scanning
// the text: same result (a term inside one word counts once per word), a
// fraction of the work on a repetitive note.
func (c *cachedNote) bodyTermCount(term string, wordTerm bool, headings float64) float64 {
	body := c.all[c.bodyOff:]
	if !strings.Contains(body, term) {
		return 0
	}
	if !wordTerm || !c.countByWords {
		return max(0, termCount(body, term)-headings)
	}
	n := 0.0
	for _, w := range c.words {
		if w.body == 0 || len(w.s) < len(term) {
			continue
		}
		switch i := strings.Index(w.s, term); {
		case i == 0:
			n += float64(w.body)
		case i > 0:
			n += float64(w.body) * midWordWeight
		}
	}
	return n
}

// termFields writes the per-field occurrence counts of term into tf.
// wordTerm is isWordTerm(term).
func (c *cachedNote) termFields(term string, wordTerm bool, tf []float64) {
	tf[fieldTitle] = termCount(c.title, term)
	tf[fieldPath] = termCount(c.path, term)
	tf[fieldHeadings] = termCount(c.headings, term)
	tf[fieldTags] = 0
	for _, tag := range c.tags {
		tf[fieldTags] += termCount(tag, term)
	}
	// The body is everything after the frontmatter except the headings,
	// which sit in all exactly once each.
	tf[fieldBody] = c.bodyTermCount(term, wordTerm, tf[fieldHeadings])
	if tf[fieldTitle]+tf[fieldPath]+tf[fieldHeadings]+tf[fieldTags]+tf[fieldBody] == 0 {
		// Matched only in another frontmatter key (status, aliases, ...):
		// still a match, counted as body text.
		tf[fieldBody] = max(termCount(c.all[:c.bodyOff], term), midWordWeight)
	}
}

// exactMatch reports whether term occurs anywhere the scan matches: title,
// a tag, the path or the raw text (frontmatter included).
func (c *cachedNote) exactMatch(term string) bool {
	if strings.Contains(c.title, term) || strings.Contains(c.path, term) || strings.Contains(c.all, term) {
		return true
	}
	for _, tag := range c.tags {
		if strings.Contains(tag, term) {
			return true
		}
	}
	return false
}

// tfStride is the slab space of one term of one match: its per-field
// counts, then the weight of its match kind (search_match.go).
const tfStride = numFields + 1

// matchBM25F is phase 1 of BM25F for one candidate. Every term must match
// (AND): exactly, or failing that through its Czech word form or within
// its typo budget (search_match.go). With allowMiss one term may match
// nothing at all (the relaxed AND). The candidate counts into the
// statistics whether it matches or not; for a match the per-field counts
// and the weight of every term's match kind go to the slab for phase 2.
func (s *ScanSearcher) matchBM25F(e *Entry, terms []string, wordTerms []bool, st *bm25Stats, allowMiss bool) (scored, bool) {
	c, err := s.content(e)
	if err != nil {
		return scored{}, false // unreadable candidates are skipped, not fatal
	}
	if st.df == nil {
		st.df = make([]int, len(terms))
	}
	st.n++
	for f, l := range c.fieldLen {
		st.lenSum[f] += int64(l)
	}

	// Exact containment of every term first: cheap, and every candidate
	// counts into the document frequencies whether it matches or not.
	var missing uint64 // terms with no exact hit (bit per term, first 64)
	allExact := true
	for i, term := range terms {
		if c.exactMatch(term) {
			st.df[i]++
			continue
		}
		allExact = false
		if i < 64 {
			missing |= 1 << i
		}
	}

	// The terms without an exact hit, in query order: a word form or a
	// typo, else the note drops out (or, relaxed, the first such term is
	// let go).
	off := len(st.slab)
	st.slab = append(st.slab, make([]float64, len(terms)*tfStride)...)
	tf := st.slab[off:]
	// match is what each term matched in the note ("" for the missing term
	// of a relaxed match). Snippets hunt for the terms plus, for a rescued
	// term, the note's word (highlighted whole) and the cut term (finds the
	// other word forms). Both are copied only once a term needs it: most
	// candidates of a typo query fail on their first term.
	match, snippetTerms := terms, terms
	own := func() {
		if len(match) > 0 && &match[0] == &terms[0] {
			match = append(make([]string, 0, len(terms)), terms...)
			snippetTerms = append(make([]string, 0, 3*len(terms)), terms...)
		}
	}
	missed := false
	for i, term := range terms {
		weight := &tf[i*tfStride+numFields]
		if allExact || (i < 64 && missing&(1<<i) == 0) || (i >= 64 && c.exactMatch(term)) {
			*weight = 1
			continue
		}
		m, w, word, ok := c.inexactMatch(term)
		if !ok {
			if !allowMiss || missed {
				st.slab = st.slab[:off]
				return scored{}, false
			}
			own()
			missed, match[i] = true, ""
			continue
		}
		own()
		st.df[i]++
		match[i], *weight = m, w
		snippetTerms = append(snippetTerms, word)
		if m != word {
			snippetTerms = append(snippetTerms, m)
		}
	}
	for i, m := range match {
		if m != "" {
			c.termFields(m, m != terms[i] || wordTerms[i], tf[i*tfStride:i*tfStride+numFields])
		}
	}
	return scored{entry: e, note: c, modTime: e.ModTime, snippetTerms: snippetTerms, tfOff: int32(off)}, true
}

// scoreBM25F is phase 2: merge the workers' statistics, score every match
// and drop those below minScore.
func scoreBM25F(results []scored, nTerms, nCandidates int, shards []bm25Stats, minScore float64, relaxed bool) []scored {
	df := make([]int, nTerms)
	var lenSum [numFields]int64
	n := 0
	for _, st := range shards {
		for i, d := range st.df {
			df[i] += d
		}
		for f := range lenSum {
			lenSum[f] += st.lenSum[f]
		}
		n += st.n
	}
	if n == 0 {
		n = nCandidates
	}

	idf := make([]float64, nTerms)
	idfSum := 0.0
	for i, d := range df {
		d = min(d, n)
		idf[i] = math.Log(1 + (float64(n-d)+0.5)/(float64(d)+0.5))
		idfSum += idf[i]
	}
	var avgLen [numFields]float64
	for f := range avgLen {
		avgLen[f] = float64(lenSum[f]) / float64(max(n, 1))
	}

	kept := results[:0]
	for _, r := range results {
		k := int(r.tfOff)
		tf := shards[r.shard].slab[k : k+nTerms*tfStride]
		if relaxed && !holdsMostOfQuery(tf, idf, idfSum) {
			continue
		}
		r.score = bm25fRelevance(r, tf, idf, idfSum, &avgLen)
		if r.score >= minScore {
			kept = append(kept, r)
		}
	}
	return kept
}

// holdsMostOfQuery reports whether the terms a relaxed match does hold
// carry more than half the query's rarity (IDF): the term it misses must be
// the less telling part of the query. A query word no note uses is the
// rarest of all, so a note that holds only the common rest is dropped
// rather than returned as a weak guess.
func holdsMostOfQuery(tfs, idf []float64, idfSum float64) bool {
	held := 0.0
	for i := range idf {
		if tfs[i*tfStride+numFields] > 0 {
			held += idf[i]
		}
	}
	return held > idfSum/2
}

// bm25fRelevance is the 0..1 relevance of one match, rounded to
// scoreDecimals; a match never rounds down to 0. Each term's saturated
// field sum is scaled by the weight of its match kind (exact 1, a word
// form, prefix or typo less, a missing term of a relaxed match 0).
func bm25fRelevance(r scored, tfs, idf []float64, idfSum float64, avgLen *[numFields]float64) float64 {
	if idfSum <= 0 {
		return 1.0 / scoreDecimals
	}
	lens := &r.note.fieldLen
	sum := 0.0
	for i := range idf {
		tf := tfs[i*tfStride : (i+1)*tfStride]
		weighted := 0.0
		for f, fl := range bm25Fields {
			if tf[f] == 0 {
				continue
			}
			norm := 1.0
			if fl.b > 0 && avgLen[f] > 0 {
				norm = 1 - fl.b + fl.b*float64(lens[f])/avgLen[f]
			}
			weighted += fl.weight * tf[f] / norm
		}
		sum += idf[i] * tf[numFields] * weighted / (bm25K1 + weighted)
	}
	rel := math.Round(sum/idfSum*scoreDecimals) / scoreDecimals
	return min(max(rel, 1.0/scoreDecimals), 1)
}
