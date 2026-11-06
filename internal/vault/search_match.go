package vault

import (
	"strings"
	"unicode/utf8"
)

// Match quality (NSO-578).
//
// BM25F (search_bm25f.go) decides how much a match weighs; this file decides
// what else counts as a match when a query term is nowhere in a note as a
// substring. Two rescues, the better one wins, and its weight multiplies
// the term's contribution to the score (an exact substring hit weighs 1):
//
//   - Czech word form: the term without its case ending, matched as the
//     start of a word of the note. "faktury" becomes "faktur", which starts
//     faktura, fakturu and faktur. Weight 0.6 · len / (len + 0.3 · excess),
//     len the runes of the cut term, excess how many more the note's word
//     has (MiniSearch's prefix weight, the library under Obsidian's
//     Omnisearch).
//   - Typo: the closest word within the term's edit budget, weight
//     typoWeight.
//
// The golden set (docs/search-quality.md) decided what stayed: weighing a
// term that only starts longer words below a whole word, a typo weight by
// edit distance, the stricter Meilisearch typo budget and a bonus for query
// words close together were measured and left out.

const (
	// prefixWeightMax and prefixExcessFactor shape the weight of a word
	// form match (MiniSearch: prefix weight, and 0.3 per extra character).
	prefixWeightMax    = 0.6
	prefixExcessFactor = 0.3
	// minPrefixRunes is the shortest cut term matched as a prefix: shorter
	// stems start too many unrelated words.
	minPrefixRunes = 4
)

// prefixWeight is the weight of a cut term of n runes matched as the start
// of a word excess runes longer.
func prefixWeight(n, excess int) float64 {
	return prefixWeightMax * float64(n) / (float64(n) + prefixExcessFactor*float64(excess))
}

// czechEndings are the case endings of Czech nouns and adjectives, folded
// (no diacritics): the "light" stemmer of Dolamic and Savoy (2009) that
// Lucene ships as CzechStemmer, without its possessive and palatalisation
// rules. Not a stemmer of its own, just its short list of endings. A group
// applies to terms of at least minRunes runes, the groups in order, and the
// first ending that fits is cut.
var czechEndings = []struct {
	minRunes int
	endings  []string
}{
	{8, []string{"atech"}},
	{7, []string{"etem", "atum"}},
	{6, []string{"ech", "ich", "eho", "emi", "emu", "ete", "eti", "iho", "imi", "imu", "ach", "ata", "aty", "ych", "ama", "ami", "ove", "ovi", "ymi"}},
	{5, []string{"em", "es", "im", "um", "at", "am", "os", "us", "ym", "mi", "ou"}},
	{4, []string{"a", "e", "i", "o", "u", "y"}},
}

// cutCzechEnding returns the folded term without its case ending, or ""
// when it has none, is not a plain word, or what remains is shorter than
// minPrefixRunes. "faktury" gives "faktur", "dlouhym" gives "dlouh",
// "psa" gives "" (too short).
func cutCzechEnding(term string) string {
	if !isWordTerm(term) {
		return ""
	}
	n := utf8.RuneCountInString(term)
	for _, g := range czechEndings {
		if n < g.minRunes {
			continue
		}
		for _, e := range g.endings {
			if strings.HasSuffix(term, e) {
				if n-len(e) < minPrefixRunes { // endings are ASCII: bytes = runes
					return ""
				}
				return term[:len(term)-len(e)]
			}
		}
	}
	return ""
}

// prefixMatch returns the weight of the best word of the note that starts
// with prefix and that word, or 0 and "" when none does. The words are the
// note's unique words (title and text, frontmatter included, no further
// pass over the text) plus those of its path and tags.
func (c *cachedNote) prefixMatch(prefix string) (float64, string) {
	n := utf8.RuneCountInString(prefix)
	best, bestWord := 0.0, ""
	try := func(w string, runes int) {
		if runes < n || !strings.HasPrefix(w, prefix) {
			return
		}
		if pw := prefixWeight(n, runes-n); pw > best {
			best, bestWord = pw, w
		}
	}
	for _, w := range c.words {
		try(w.s, int(w.rlen))
	}
	each := func(w string) { try(w, utf8.RuneCountInString(w)) }
	if strings.Contains(c.path, prefix) {
		forEachWord(c.path, each)
	}
	for _, t := range c.tags {
		if strings.Contains(t, prefix) {
			forEachWord(t, each)
		}
	}
	return best, bestWord
}

// inexactMatch looks for a term the note does not contain as a substring:
// through its Czech word form or within its typo budget. It returns what to
// count in the note (the cut term or the note's word), the weight of the
// better rescue, the note's word to highlight in snippets, and false when
// neither matches. The typo search is skipped when the word form already
// beats any typo.
func (c *cachedNote) inexactMatch(term string) (match string, weight float64, word string, ok bool) {
	if cut := cutCzechEnding(term); cut != "" {
		if w, best := c.prefixMatch(cut); w > 0 {
			match, weight, word = cut, w, best
		}
	}
	if weight >= typoWeight {
		return match, weight, word, true
	}
	if w, _ := fuzzyWord(c, term); w != "" {
		match, weight, word = w, typoWeight, w
	}
	return match, weight, word, weight > 0
}
