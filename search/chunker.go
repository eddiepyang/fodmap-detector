package search

import "strings"

// defaultSeparators is the hierarchy of text boundaries used by ChunkText,
// ordered from most to least desirable split points.
var defaultSeparators = []string{"\n\n", "\n", ". ", "! ", "? ", "; ", ", ", " ", ""}

// ChunkText splits text into chunks of at most maxChars runes using recursive
// character text splitting. It tries to split on natural boundaries (paragraphs,
// then sentences, then words) before falling back to raw character splits.
//
// overlap controls how many characters from the end of one chunk are repeated
// at the start of the next (set to 0 if splitting on natural boundaries is
// sufficient). The overlap is only applied when a split actually occurs.
func ChunkText(text string, maxChars, overlap int) []string {
	if maxChars <= 0 {
		return []string{text}
	}
	if overlap < 0 {
		overlap = 0
	}
	if overlap >= maxChars {
		overlap = maxChars - 1
	}

	runes := []rune(text)
	if len(runes) <= maxChars {
		return []string{text}
	}

	return recursiveSplit(text, defaultSeparators, maxChars, overlap)
}

// recursiveSplit is the core recursive algorithm. It tries splitting on the
// first separator in the list; if any resulting piece is still too long, it
// recurses with the next separator.
func recursiveSplit(text string, separators []string, maxChars, overlap int) []string {
	if len([]rune(text)) <= maxChars {
		return []string{text}
	}

	// Pick the best separator: the first one in the hierarchy that actually
	// appears in the text.
	sep := ""
	remaining := separators
	for i, s := range separators {
		if s == "" || strings.Contains(text, s) {
			sep = s
			remaining = separators[i+1:]
			break
		}
	}

	var pieces []string
	if sep == "" {
		// Last resort: split by individual runes.
		runes := []rune(text)
		for i := 0; i < len(runes); i += maxChars {
			end := i + maxChars
			if end > len(runes) {
				end = len(runes)
			}
			pieces = append(pieces, string(runes[i:end]))
		}
		return pieces
	}

	splits := strings.Split(text, sep)

	// Merge small consecutive splits back together until they approach maxChars.
	var chunks []string
	var current strings.Builder

	for i, part := range splits {
		candidate := part
		if i < len(splits)-1 {
			candidate += sep
		}

		if current.Len() == 0 {
			current.WriteString(candidate)
			continue
		}

		merged := current.String() + candidate
		if len([]rune(merged)) <= maxChars {
			current.Reset()
			current.WriteString(merged)
		} else {
			// Flush current chunk, trimming trailing whitespace/separators.
			chunks = append(chunks, strings.TrimSpace(current.String()))
			current.Reset()
			current.WriteString(candidate)
		}
	}
	if current.Len() > 0 {
		chunks = append(chunks, strings.TrimSpace(current.String()))
	}

	// Recurse on any chunk that is still too long.
	var result []string
	for _, chunk := range chunks {
		if len([]rune(chunk)) > maxChars {
			result = append(result, recursiveSplit(chunk, remaining, maxChars, overlap)...)
		} else {
			result = append(result, chunk)
		}
	}

	// Apply overlap between adjacent chunks.
	if overlap > 0 && len(result) > 1 {
		result = applyOverlap(result, overlap)
	}

	return result
}

// applyOverlap prepends the last `overlap` runes of each chunk to the
// beginning of the following chunk, without exceeding original chunk sizes.
func applyOverlap(chunks []string, overlap int) []string {
	out := make([]string, len(chunks))
	out[0] = chunks[0]
	for i := 1; i < len(chunks); i++ {
		prevRunes := []rune(chunks[i-1])
		start := len(prevRunes) - overlap
		if start < 0 {
			start = 0
		}
		prefix := string(prevRunes[start:])
		out[i] = prefix + chunks[i]
	}
	return out
}
