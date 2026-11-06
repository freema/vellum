package vault

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"slices"
	"testing"
	"time"
)

// benchSearcher builds an n-note corpus (~40 lines each, Czech diacritics
// included, a needle in every 50th note) and warms the content cache.
func benchSearcher(b *testing.B, n int) *ScanSearcher {
	b.Helper()
	v := newTestVault(b)
	for i := 0; i < n; i++ {
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
	s := benchSearcher(b, 2000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.Search("jehla", SearchOpts{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSearchDiacriticsWarm(b *testing.B) {
	s := benchSearcher(b, 2000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// ASCII query against accented content — folding path.
		if _, err := s.Search("ukoly prilohami", SearchOpts{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSearchTypoWarm(b *testing.B) {
	s := benchSearcher(b, 2000)
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
	s := benchSearcher(b, 2000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// No exact nor fuzzy hit anywhere — full scan, empty result.
		if _, err := s.Search("xqzwrtplk", SearchOpts{}); err != nil {
			b.Fatal(err)
		}
	}
}

// searchBenchCases are the four warm cases measured on the 2 000-note corpus
// above, reused for the 5 000-note latency and memory run, plus the cost of
// the relaxed AND (NSO-578): two terms, one of them nowhere, so the strict
// scan finds nothing and a second, relaxed scan runs (and drops every note,
// since the unknown term outweighs the rest).
var searchBenchCases = []struct{ name, query string }{
	{"exact", "jehla"},
	{"diacritics", "ukoly prilohami"},
	{"typo", "jehlla"},
	{"nomatch", "xqzwrtplk"},
	{"relaxed", "jehla xqzwrtplk"},
}

// BenchmarkSearch5k runs the four warm cases on a 5 000-note corpus and
// reports, next to ns/op, the latency percentiles (p50-ms, p95-ms), the heap
// the warm search state keeps between queries (idle-MiB: index plus content
// cache) and the heap high-water mark while one query runs (peak-MiB, idle
// plus everything the query allocates, measured with the GC off). Use a fixed
// iteration count so the percentiles have enough samples:
//
//	go test ./internal/vault -run '^$' -bench BenchmarkSearch5k -benchtime 300x
func BenchmarkSearch5k(b *testing.B) { benchSearch5k(b, RankerBM25F) }

// BenchmarkSearch5kLegacy is BenchmarkSearch5k with the legacy ranker, to
// compare both on the same machine and load while BM25F rolls out.
func BenchmarkSearch5kLegacy(b *testing.B) { benchSearch5k(b, RankerLegacy) }

func benchSearch5k(b *testing.B, ranker string) {
	s := benchSearcher(b, 5000)
	if err := s.SetRanker(ranker); err != nil {
		b.Fatal(err)
	}
	idle := retainedHeap()
	for _, c := range searchBenchCases {
		b.Run(c.name, func(b *testing.B) {
			lat := make([]time.Duration, 0, b.N)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				t0 := time.Now()
				if _, err := s.Search(c.query, SearchOpts{}); err != nil {
					b.Fatal(err)
				}
				lat = append(lat, time.Since(t0))
			}
			b.StopTimer()
			slices.Sort(lat)
			b.ReportMetric(durMs(latencyPercentile(lat, 50)), "p50-ms")
			b.ReportMetric(durMs(latencyPercentile(lat, 95)), "p95-ms")
			b.ReportMetric(bytesMiB(idle), "idle-MiB")
			b.ReportMetric(bytesMiB(peakSearchHeap(b, s, c.query)), "peak-MiB")
		})
	}
}

// peakSearchHeap is the heap in use at the end of one query run with the
// garbage collector switched off: the retained state plus every byte the
// query allocated, an upper bound of the real peak.
func peakSearchHeap(b *testing.B, s Searcher, query string) int64 {
	b.Helper()
	defer debug.SetGCPercent(debug.SetGCPercent(-1))
	runtime.GC()
	if _, err := s.Search(query, SearchOpts{}); err != nil {
		b.Fatal(err)
	}
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return int64(m.HeapAlloc)
}
