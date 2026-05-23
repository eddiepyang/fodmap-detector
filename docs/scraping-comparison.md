# Scraping Strategy: Restaurant Menus

This document outlines the comparative strategies for scraping restaurant menus to feed into the FODMAP/allergen RAG pipeline, comparing languages and modern API services.

## Language Comparison: Go vs. Python

When building web scrapers, the choice of language depends entirely on the target sites and the required scale.

### Go (Best for Speed and Scale)
If the goal is to crawl thousands of sites rapidly or hit well-structured APIs, Go is superior.
- **Tools**: `colly` (crawling framework), `goquery` (HTML parsing).
- **Pros**: Unmatched concurrency via goroutines, incredibly low memory footprint, and it integrates directly into our existing backend without standing up a separate service.
- **Cons**: Handling JavaScript-heavy Single Page Applications (SPAs) requires `chromedp`, which is verbose and more brittle than modern Python alternatives.

### Python (Best for Complex, Messy Pages)
If the target sites are messy, heavily reliant on JavaScript (e.g., React/Vue SPAs), or behind complex bot protection, Python's ecosystem is unmatched.
- **Tools**: `Playwright`, `BeautifulSoup`, `Scrapy`.
- **Pros**: Playwright makes rendering JS and interacting with elements trivial. `BeautifulSoup` is highly forgiving with broken HTML.
- **Cons**: Much slower, high memory overhead, and requires a separate Python service deployment alongside the Go backend.

---

## Scraping API Services (The Modern RAG Approach)

For modern RAG pipelines, managing custom scrapers is often avoided in favor of scraping APIs that return clean Markdown or JSON. This offloads the burden of JS rendering, proxy management, and anti-bot evasion.

### Firecrawl (Recommended for Menus)
Restaurant menus are notoriously difficult. They come in embedded PDFs, images, dynamic widgets (Toast, Square), or heavily stylized HTML where prices are visually disconnected from items. 

Firecrawl excels here because it is designed for **structured extraction**.

- **How it works**: You provide a URL and a JSON schema (e.g., `"return a JSON array with dish_name, price, description, dietary_tags"`). Firecrawl uses LLMs under the hood to parse the messy visual layout into that clean JSON structure.
- **Dynamic Sites**: Effortlessly handles JS rendering and can click buttons (e.g., "Expand Menu").
- **Cost**: Operates on a monthly credit system. Basic scraping is 1 credit/page, but the AI Extraction feature (required for turning messy menus into clean JSON) costs a premium (5x-10x the base cost).
- **Use Case**: Best if we need precise analysis on ingredients per dish, requiring structured data over a wall of text.

### Jina Reader (The Budget Option)
Jina Reader acts as an advanced headless browser that extracts visible text and converts it directly to Markdown.

- **How it works**: Prepend `https://r.jina.ai/` to a URL, and it returns a Markdown string.
- **Limitations**: For a simple text menu, it works well. However, for complex layouts, it may return a wall of text where the price is separated from the dish by unrelated navigation links. It also struggles with image-only menus (JPEGs) without explicit OCR prompting.
- **Cost**: Billed by tokens, not pages. Extremely cheap ($0.05 per 1 million tokens). It also has a free tier that doesn't even require an API key if you stay under 20 requests per minute.
- **Use Case**: Best if we are just feeding raw text directly into Weaviate as giant chunks of context for Gemini to process later, though Gemini may hallucinate if the Markdown formatting scrambles the relationship between dishes and their ingredients.

## Verdict

For the `fodmap-detector` pipeline:
1. **To build a massive database of structured menu items**: Call the **Firecrawl API** from the Go backend. It avoids managing a Python service and guarantees clean JSON.
2. **For generic, cheap context ingestion**: Call the **Jina Reader API** from the Go backend, but be prepared to handle messy Markdown formatting in the downstream LLM prompts.
