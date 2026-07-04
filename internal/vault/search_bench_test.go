package vault

import (
	"fmt"
	"testing"
)

// benchSearcher builds a 2000-note corpus (~40 lines each, Czech diacritics
// included) and warms the content cache.
func benchSearcher(b *testing.B) *ScanSearcher {
	b.Helper()
	v := newTestVault(b)
	for i := 0; i < 2000; i++ {
		body := "---\ntags: [poznamky, t" + fmt.Sprint(i%7) + "]\n---\n# Poznámka číslo " + fmt.Sprint(i) + "\n\n"
		for l := 0; l < 40; l++ {
			body += "Běžný řádek s několika slovy, úkoly a přílohami k projektu.\n"
		}
		if i%50 == 0 {
			body += "Tady je jehla kterou hledáme.\n"
		}
		mustWrite(b, v, fmt.Sprintf("bulk/p%d/n%d.md", i%25, i), body)
	}
	ix := NewIndex(v)
	if err := ix.Build(); err != nil {
		b.Fatal(err)
	}
	s := NewScanSearcher(v, ix)
	if _, err := s.Search("jehla", SearchOpts{}); err != nil { // warm the cache
		b.Fatal(err)
	}
	return s
}

func BenchmarkSearchExactWarm(b *testing.B) {
	s := benchSearcher(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.Search("jehla", SearchOpts{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSearchDiacriticsWarm(b *testing.B) {
	s := benchSearcher(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// ASCII query against accented content — folding path.
		if _, err := s.Search("ukoly prilohami", SearchOpts{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSearchTypoWarm(b *testing.B) {
	s := benchSearcher(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// One typo → substring fails everywhere → every note pays the
		// Levenshtein fallback. This is the worst case.
		if _, err := s.Search("jehlla", SearchOpts{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSearchNoMatchWarm(b *testing.B) {
	s := benchSearcher(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// No exact nor fuzzy hit anywhere — full scan, empty result.
		if _, err := s.Search("xqzwrtplk", SearchOpts{}); err != nil {
			b.Fatal(err)
		}
	}
}
