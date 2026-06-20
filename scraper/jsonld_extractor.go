package scraper

import (
	"encoding/json"
	"io"
	"strings"

	"golang.org/x/net/html"
)

// JSONLDMeta holds restaurant metadata harvested from JSON-LD even when no
// hasMenu is present. This allows populating city/state on the result.
type JSONLDMeta struct {
	RestaurantName string
	City           string
	State          string
}

// ExtractJSONLD scans the HTML body for <script type="application/ld+json">
// blocks and extracts menu items (Tier 0 fast-path) and restaurant metadata.
//
// It returns (items, meta, ok). ok is true when at least one menu item was
// found — callers skip Tier 1 in that case. meta is always populated when
// Restaurant schema is present, even on fall-through.
func ExtractJSONLD(r io.Reader) ([]MenuEntry, JSONLDMeta, bool) {
	doc, err := html.Parse(r)
	if err != nil {
		return nil, JSONLDMeta{}, false
	}

	var blocks []string
	collectJSONLD(doc, &blocks)

	var allItems []MenuEntry
	var meta JSONLDMeta

	for _, block := range blocks {
		var raw any
		if err := json.Unmarshal([]byte(block), &raw); err != nil {
			continue
		}

		// Handle @graph arrays.
		if m, ok := raw.(map[string]any); ok {
			if graph, ok := m["@graph"].([]any); ok {
				for _, node := range graph {
					if nm, ok := node.(map[string]any); ok {
						items, m2 := processNode(nm)
						allItems = append(allItems, items...)
						if meta.RestaurantName == "" {
							meta = m2
						}
					}
				}
				continue
			}
		}

		// Handle top-level array.
		if arr, ok := raw.([]any); ok {
			for _, node := range arr {
				if nm, ok := node.(map[string]any); ok {
					items, m2 := processNode(nm)
					allItems = append(allItems, items...)
					if meta.RestaurantName == "" {
						meta = m2
					}
				}
			}
			continue
		}

		// Handle single object.
		if m, ok := raw.(map[string]any); ok {
			items, m2 := processNode(m)
			allItems = append(allItems, items...)
			if meta.RestaurantName == "" {
				meta = m2
			}
		}
	}

	return allItems, meta, len(allItems) > 0
}

// collectJSONLD traverses the HTML tree and appends the text content of every
// <script type="application/ld+json"> element to blocks.
func collectJSONLD(n *html.Node, blocks *[]string) {
	if n.Type == html.ElementNode && n.Data == "script" {
		for _, a := range n.Attr {
			if a.Key == "type" && strings.EqualFold(a.Val, "application/ld+json") {
				if n.FirstChild != nil {
					*blocks = append(*blocks, n.FirstChild.Data)
				}
				break
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		collectJSONLD(c, blocks)
	}
}

// processNode extracts menu items and restaurant metadata from a single JSON-LD
// object.
func processNode(m map[string]any) ([]MenuEntry, JSONLDMeta) {
	t, _ := m["@type"].(string)
	var items []MenuEntry
	var meta JSONLDMeta

	switch t {
	case "Restaurant", "FoodEstablishment", "CafeOrCoffeeShop", "FastFoodRestaurant",
		"BarOrPub", "Bakery", "Brewery", "Winery":
		meta = extractRestaurantMeta(m)
		if hasMenu, ok := m["hasMenu"].(map[string]any); ok {
			items = extractMenuItems(hasMenu)
		}
		if menuURL, ok := m["menu"].(string); ok && menuURL != "" && len(items) == 0 {
			// menu is a URL; we can't follow it here but record metadata.
			_ = menuURL
		}

	case "Menu":
		items = extractMenuItems(m)

	case "MenuItem":
		if entry, ok := menuItemToEntry(m); ok {
			items = append(items, entry)
		}
	}

	return items, meta
}

// extractRestaurantMeta reads name, address.addressLocality and
// address.addressRegion from a Restaurant-type node.
func extractRestaurantMeta(m map[string]any) JSONLDMeta {
	var meta JSONLDMeta
	if name, ok := m["name"].(string); ok {
		meta.RestaurantName = name
	}
	if addr, ok := m["address"].(map[string]any); ok {
		if city, ok := addr["addressLocality"].(string); ok {
			meta.City = city
		}
		if state, ok := addr["addressRegion"].(string); ok {
			meta.State = state
		}
	}
	return meta
}

// extractMenuItems walks hasMenuSection[].hasMenuItem[] and collects entries.
func extractMenuItems(menu map[string]any) []MenuEntry {
	var items []MenuEntry

	sections, _ := menu["hasMenuSection"].([]any)
	for _, s := range sections {
		sec, ok := s.(map[string]any)
		if !ok {
			continue
		}
		menuItems, _ := sec["hasMenuItem"].([]any)
		for _, mi := range menuItems {
			mim, ok := mi.(map[string]any)
			if !ok {
				continue
			}
			if entry, ok := menuItemToEntry(mim); ok {
				items = append(items, entry)
			}
		}
	}

	// Also handle direct hasMenuItem at the Menu level.
	direct, _ := menu["hasMenuItem"].([]any)
	for _, mi := range direct {
		mim, ok := mi.(map[string]any)
		if !ok {
			continue
		}
		if entry, ok := menuItemToEntry(mim); ok {
			items = append(items, entry)
		}
	}

	return items
}

// menuItemToEntry converts a MenuItem JSON-LD object to a MenuEntry.
func menuItemToEntry(m map[string]any) (MenuEntry, bool) {
	name, _ := m["name"].(string)
	if name == "" {
		return MenuEntry{}, false
	}
	desc, _ := m["description"].(string)

	var ingredients []string
	// suitableForDiet can hint at dietary properties but isn't ingredients.
	// Look for an explicit "ingredients" or "recipeIngredient" field.
	if raw, ok := m["ingredients"]; ok {
		ingredients = toStringSlice(raw)
	} else if raw, ok := m["recipeIngredient"]; ok {
		ingredients = toStringSlice(raw)
	}

	return MenuEntry{
		DishName:           name,
		Description:        desc,
		StatedIngredients:  ingredients,
		HasFullIngredients: desc != "",
	}, true
}

// toStringSlice converts an any that is either a []any of
// strings or a plain string into a []string.
func toStringSlice(v any) []string {
	switch tv := v.(type) {
	case []any:
		var out []string
		for _, item := range tv {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		return []string{tv}
	}
	return nil
}
