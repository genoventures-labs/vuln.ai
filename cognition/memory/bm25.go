package memory

import (
	"math"
	"sort"
	"strings"
	"sync"
	"unicode"
)

const (
	bm25K1 = 1.2
	bm25B  = 0.75
)

type bm25Document struct {
	ID       string
	Content  string
	Metadata map[string]string
	TF       map[string]int
	Length   int
}

type bm25Result struct {
	ID       string
	Content  string
	Metadata map[string]string
	Score    float64
}

// BM25Index is an in-memory Okapi BM25 index for lexical retrieval.
type BM25Index struct {
	mu sync.RWMutex

	docs     map[string]bm25Document
	termDF   map[string]int
	postings map[string]map[string]int // term -> docID -> tf
	avgLen   float64
}

func NewBM25Index() *BM25Index {
	return &BM25Index{
		docs:     make(map[string]bm25Document),
		termDF:   make(map[string]int),
		postings: make(map[string]map[string]int),
	}
}

func (idx *BM25Index) Rebuild(docs []bm25Document) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	idx.docs = make(map[string]bm25Document, len(docs))
	idx.termDF = make(map[string]int)
	idx.postings = make(map[string]map[string]int)

	totalLen := 0
	for _, d := range docs {
		if strings.TrimSpace(d.ID) == "" {
			continue
		}
		tf, length := termFreq(tokenizeBM25(d.Content))
		d.TF = tf
		d.Length = length
		idx.docs[d.ID] = d
		totalLen += d.Length

		for term := range tf {
			idx.termDF[term]++
			if idx.postings[term] == nil {
				idx.postings[term] = make(map[string]int)
			}
			idx.postings[term][d.ID] = tf[term]
		}
	}

	if len(idx.docs) > 0 {
		idx.avgLen = float64(totalLen) / float64(len(idx.docs))
	} else {
		idx.avgLen = 0
	}
}

func (idx *BM25Index) Upsert(doc bm25Document) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	if strings.TrimSpace(doc.ID) == "" {
		return
	}
	if old, ok := idx.docs[doc.ID]; ok {
		idx.removeDocLocked(old)
	}
	idx.addDocLocked(doc)
}

func (idx *BM25Index) Search(query string, limit int, namespace string) []bm25Result {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if limit <= 0 || len(idx.docs) == 0 {
		return nil
	}
	terms := tokenizeBM25(query)
	if len(terms) == 0 {
		return nil
	}

	uniqueTerms := dedupeTerms(terms)
	scores := make(map[string]float64)
	N := float64(len(idx.docs))
	avgLen := idx.avgLen
	if avgLen <= 0 {
		avgLen = 1
	}

	for _, term := range uniqueTerms {
		df := float64(idx.termDF[term])
		if df == 0 {
			continue
		}
		idf := math.Log(1.0 + ((N - df + 0.5) / (df + 0.5)))
		for docID, tf := range idx.postings[term] {
			doc := idx.docs[docID]
			if namespace != "" && strings.TrimSpace(doc.Metadata["namespace"]) != namespace {
				continue
			}
			denom := float64(tf) + bm25K1*(1.0-bm25B+bm25B*(float64(maxInt(doc.Length, 1))/avgLen))
			if denom <= 0 {
				continue
			}
			scores[docID] += idf * ((float64(tf) * (bm25K1 + 1.0)) / denom)
		}
	}
	if len(scores) == 0 {
		return nil
	}

	results := make([]bm25Result, 0, len(scores))
	for docID, score := range scores {
		d := idx.docs[docID]
		meta := make(map[string]string, len(d.Metadata))
		for k, v := range d.Metadata {
			meta[k] = v
		}
		results = append(results, bm25Result{
			ID:       docID,
			Content:  d.Content,
			Metadata: meta,
			Score:    score,
		})
	}

	sort.SliceStable(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	if len(results) > limit {
		results = results[:limit]
	}
	return normalizeBM25Results(results)
}

func (idx *BM25Index) addDocLocked(doc bm25Document) {
	tf, length := termFreq(tokenizeBM25(doc.Content))
	doc.TF = tf
	doc.Length = length
	idx.docs[doc.ID] = doc

	for term := range tf {
		idx.termDF[term]++
		if idx.postings[term] == nil {
			idx.postings[term] = make(map[string]int)
		}
		idx.postings[term][doc.ID] = tf[term]
	}
	idx.recomputeAvgLenLocked()
}

func (idx *BM25Index) removeDocLocked(doc bm25Document) {
	delete(idx.docs, doc.ID)
	for term := range doc.TF {
		if idx.termDF[term] > 0 {
			idx.termDF[term]--
		}
		if post := idx.postings[term]; post != nil {
			delete(post, doc.ID)
			if len(post) == 0 {
				delete(idx.postings, term)
			}
		}
		if idx.termDF[term] <= 0 {
			delete(idx.termDF, term)
		}
	}
	idx.recomputeAvgLenLocked()
}

func (idx *BM25Index) recomputeAvgLenLocked() {
	if len(idx.docs) == 0 {
		idx.avgLen = 0
		return
	}
	totalLen := 0
	for _, d := range idx.docs {
		totalLen += d.Length
	}
	idx.avgLen = float64(totalLen) / float64(len(idx.docs))
}

func normalizeBM25Results(in []bm25Result) []bm25Result {
	if len(in) == 0 {
		return nil
	}
	maxScore := 0.0
	for _, r := range in {
		if r.Score > maxScore {
			maxScore = r.Score
		}
	}
	if maxScore <= 0 {
		return in
	}
	out := make([]bm25Result, 0, len(in))
	for _, r := range in {
		r.Score = clamp01(r.Score / maxScore)
		out = append(out, r)
	}
	return out
}

func tokenizeBM25(text string) []string {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return nil
	}
	clean := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		if unicode.IsSpace(r) {
			return r
		}
		return ' '
	}, text)

	parts := strings.Fields(clean)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if len(p) < 2 {
			continue
		}
		out = append(out, p)
	}
	return out
}

func termFreq(tokens []string) (map[string]int, int) {
	tf := make(map[string]int, len(tokens))
	for _, t := range tokens {
		tf[t]++
	}
	return tf, len(tokens)
}

func dedupeTerms(tokens []string) []string {
	if len(tokens) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(tokens))
	out := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}
