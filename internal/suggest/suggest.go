// Package suggest finds the name a word may have meant, for the "Did you mean"
// line upstream adds to an error about an unknown name. It is pure.
package suggest

// MaxDistance bounds a suggestion: a name is suggested only when its edit
// distance from the word is below this, as upstream's search has it.
const MaxDistance = 3

// Closest returns the candidate closest to word by edit distance, if one is
// closer than MaxDistance. On a tie the first candidate in byte order wins, as
// upstream's search over its sorted names does. Candidates may be in any order.
func Closest(word string, candidates []string) (string, bool) {
	best, bestDistance := "", MaxDistance
	for _, c := range candidates {
		d := editDistance(word, c)
		if d < bestDistance || (d == bestDistance && d < MaxDistance && c < best) {
			best, bestDistance = c, d
		}
	}
	return best, best != ""
}

// editDistance is the Levenshtein distance between a and b, counted in runes
// as upstream's strsim::levenshtein counts characters. Pure.
func editDistance(a, b string) int {
	x, y := []rune(a), []rune(b)
	row := make([]int, len(y)+1)
	for j := range row {
		row[j] = j
	}
	for i := 1; i <= len(x); i++ {
		diagonal := row[0]
		row[0] = i
		for j := 1; j <= len(y); j++ {
			cost := 1
			if x[i-1] == y[j-1] {
				cost = 0
			}
			diagonal, row[j] = row[j], min(row[j]+1, row[j-1]+1, diagonal+cost)
		}
	}
	return row[len(y)]
}
