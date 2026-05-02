package scraper

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// Extractor parses structured menu data from raw HTML using heuristics,
// Schema.org microdata, and common CSS patterns.
//
// This is the primary extraction path for plain HTML menus. If it yields
// fewer than minItems results, the caller should fall back to vision.
type Extractor struct {
	minItems int
	logger   *slog.Logger
}

// NewExtractor creates an Extractor. minItems controls the threshold below
// which extraction is considered to have "failed" (triggers vision fallback).
func NewExtractor(minItems int, logger *slog.Logger) *Extractor {
	if minItems <= 0 {
		minItems = 3
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Extractor{minItems: minItems, logger: logger}
}

var (
	pricePattern = regexp.MustCompile(`\$\s*\d+(?:\.\d{2})?`)
	// Common CSS class/id patterns that signal a menu item container.
	menuItemSelectors = []string{
		"menu-item", "menu_item", "menuitem",
		"dish", "food-item", "product-item",
		"menu-card", "item-card",
	}
	// Category header words — used to track current section while walking the tree.
	categoryWords = []string{
		"appetizer", "starter", "soup", "salad", "entree", "entrée", "main",
		"pasta", "pizza", "burger", "sandwich", "taco", "sushi", "roll",
		"dessert", "sweet", "beverage", "drink", "cocktail", "wine", "beer",
		"brunch", "breakfast", "lunch", "dinner", "sides", "specials",
	}
)

// Extract parses htmlBody and returns a list of menu items.
// Returns (items, nil) on success. If len(items) < minItems, callers
// should treat this as a soft failure and try vision transcription.
func (e *Extractor) Extract(ctx context.Context, htmlBody []byte) ([]MenuItem, error) {
	doc, err := html.Parse(strings.NewReader(string(htmlBody)))
	if err != nil {
		return nil, fmt.Errorf("parsing HTML: %w", err)
	}

	// Strategy 1: Schema.org structured data (most reliable).
	items := extractSchemaOrg(doc)
	if len(items) >= e.minItems {
		e.logger.Info("extracted via Schema.org", "count", len(items))
		return items, nil
	}

	// Strategy 2: Known CSS class/id patterns.
	items = extractByClassPatterns(doc)
	if len(items) >= e.minItems {
		e.logger.Info("extracted via CSS patterns", "count", len(items))
		return items, nil
	}

	// Strategy 3: Heuristic walk — find price-adjacent text blocks.
	items = extractHeuristic(doc)
	if len(items) >= e.minItems {
		e.logger.Info("extracted via heuristic", "count", len(items))
		return items, nil
	}

	e.logger.Warn("extraction yielded few items, vision fallback recommended",
		"count", len(items), "min", e.minItems)
	return items, nil // Return what we have; caller decides.
}

// ---- Strategy 1: Schema.org ----

// extractSchemaOrg looks for <script type="application/ld+json"> blocks and
// attempts to parse Menu / ItemList / MenuItem schemas.
func extractSchemaOrg(doc *html.Node) []MenuItem {
	var items []MenuItem
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "script" {
			for _, a := range n.Attr {
				if a.Key == "type" && a.Val == "application/ld+json" {
					if n.FirstChild != nil {
						items = append(items, parseJSONLD(n.FirstChild.Data)...)
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return items
}

// parseJSONLD extracts MenuItem entries from a JSON-LD blob.
// Handles @type: "Menu", "MenuSection", "MenuItem", and "ItemList".
func parseJSONLD(raw string) []MenuItem {
	// We use a flexible map approach rather than full struct binding because
	// Schema.org Menu can be deeply nested in many ways.
	var blob interface{}
	if err := parseJSON(raw, &blob); err != nil {
		return nil
	}
	return extractFromLD(blob)
}

func extractFromLD(v interface{}) []MenuItem {
	var items []MenuItem
	switch val := v.(type) {
	case map[string]interface{}:
		t, _ := val["@type"].(string)
		switch t {
		case "MenuItem":
			item := MenuItem{}
			if name, ok := val["name"].(string); ok {
				item.Name = name
			}
			if desc, ok := val["description"].(string); ok {
				item.Description = desc
			}
			if offers, ok := val["offers"].(map[string]interface{}); ok {
				if price, ok := offers["price"].(string); ok {
					item.Price = "$" + price
				}
			}
			if item.Name != "" {
				items = append(items, item)
			}
		default:
			// Recurse into all values.
			for _, sub := range val {
				items = append(items, extractFromLD(sub)...)
			}
		}
	case []interface{}:
		for _, sub := range val {
			items = append(items, extractFromLD(sub)...)
		}
	}
	return items
}

// ---- Strategy 2: CSS class patterns ----

func extractByClassPatterns(doc *html.Node) []MenuItem {
	var items []MenuItem
	var currentCategory string

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			cls := getAttr(n, "class") + " " + getAttr(n, "id")
			cls = strings.ToLower(cls)

			// Check if this node is a category header.
			if isHeading(n.Data) {
				text := strings.TrimSpace(textContent(n))
				if isCategoryHeader(text) {
					currentCategory = text
				}
			}

			// Check if this looks like a menu-item container.
			if matchesAny(cls, menuItemSelectors) {
				if item := parseItemNode(n, currentCategory); item != nil {
					items = append(items, *item)
					return // Don't recurse into already-matched containers.
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return items
}

// parseItemNode extracts a MenuItem from a node identified as a menu item container.
func parseItemNode(n *html.Node, category string) *MenuItem {
	text := textContent(n)
	if strings.TrimSpace(text) == "" {
		return nil
	}
	item := &MenuItem{Category: category}

	// Try to find a name from heading children.
	var findName func(*html.Node)
	findName = func(child *html.Node) {
		if item.Name != "" {
			return
		}
		if child.Type == html.ElementNode && isHeading(child.Data) {
			item.Name = strings.TrimSpace(textContent(child))
			return
		}
		for c := child.FirstChild; c != nil; c = c.NextSibling {
			findName(c)
		}
	}
	findName(n)

	// Look for a price.
	if m := pricePattern.FindString(text); m != "" {
		item.Price = m
	}

	// If no heading name found, use the first line of text.
	if item.Name == "" {
		lines := strings.Split(strings.TrimSpace(text), "\n")
		for _, l := range lines {
			l = strings.TrimSpace(l)
			if l != "" {
				item.Name = l
				break
			}
		}
	}

	if item.Name == "" {
		return nil
	}
	return item
}

// ---- Strategy 3: Heuristic (price adjacency) ----

func extractHeuristic(doc *html.Node) []MenuItem {
	// Collect all text nodes that contain a price.
	var priceNodes []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode && pricePattern.MatchString(n.Data) {
			priceNodes = append(priceNodes, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	var items []MenuItem
	seen := make(map[string]bool)
	for _, pn := range priceNodes {
		// Walk up to find a plausible item container (a parent with more text).
		container := pn.Parent
		for container != nil {
			txt := strings.TrimSpace(textContent(container))
			if len(strings.Fields(txt)) >= 3 {
				break
			}
			container = container.Parent
		}
		if container == nil {
			continue
		}
		lines := strings.Split(strings.TrimSpace(textContent(container)), "\n")
		if len(lines) == 0 {
			continue
		}
		name := strings.TrimSpace(lines[0])
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		price := pricePattern.FindString(textContent(container))
		items = append(items, MenuItem{Name: name, Price: price})
	}
	return items
}

// ---- HTML tree helpers ----

func getAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func isHeading(tag string) bool {
	switch tag {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		return true
	}
	return false
}

func isCategoryHeader(text string) bool {
	lower := strings.ToLower(text)
	for _, cw := range categoryWords {
		if strings.Contains(lower, cw) {
			return true
		}
	}
	return false
}

func matchesAny(s string, patterns []string) bool {
	for _, p := range patterns {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

// textContent recursively concatenates all text nodes under n.
func textContent(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			b.WriteString(" ")
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

// parseJSON decodes a JSON string into v.
func parseJSON(s string, v interface{}) error {
	return decodeJSON(s, v)
}
