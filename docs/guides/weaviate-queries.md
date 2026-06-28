# Weaviate Query Guide

This guide covers examples of how to directly query the local Weaviate instance using its GraphQL API. This is useful for debugging vector search, checking metadata extraction accuracy, and verifying indexing.

## Basic Semantic Search (nearText)

Perform a basic semantic search over the `YelpReview` collection. This searches for concepts similar to "duck" and returns the first 10 matches, filtering by categories that include "chinese".

```bash
curl -s -X POST http://localhost:8090/v1/graphql \
  -H 'Content-Type: application/json' \
  -d '{
    "query": "{ Get { YelpReview(nearText: {concepts: [\"duck\"]}, limit: 10, where: {path: [\"categories\"], operator: equals, valueText: \"*chinese*\"}) { businessId businessName city state _additional { certainty distance } } } }"
  }' | jq .
```

## Exact Match Filtering (Equal)

Search for concepts similar to "duck", but enforce an exact case-sensitive match on the category "Chinese".

```bash
curl -s -X POST http://localhost:8090/v1/graphql \
  -H 'Content-Type: application/json' \
  -d '{
    "query": "{ Get { YelpReview(nearText: {concepts: [\"duck\"]}, limit: 10, where: {path: [\"categories\"], operator: Equal, valueText: \"Chinese\"}) { businessId businessName city state _additional { certainty distance } } } }"
  }' | jq '.'
```

## Compound Filtering (And)

Search using multiple filters at once. For example, find concepts similar to "duck" where the category is exactly "Chinese" AND the city is exactly "New York".

```bash
curl -s -X POST http://localhost:8090/v1/graphql \
  -H 'Content-Type: application/json' \
  -d '{
    "query": "{ Get { YelpReview(nearText: {concepts: [\"duck\"]}, limit: 10, where: { operator: And, operands: [ { path: [\"categories\"], operator: Equal, valueText: \"Chinese\" }, { path: [\"city\"], operator: Equal, valueText: \"New York\" } ] }) { businessId businessName city state _additional { certainty distance } } } }"
  }' | jq '.'
```

## Aggregations (GroupBy)

List all unique cities in the dataset and count how many reviews exist in each city. This query sorts them descending by review count and takes the top 20.

```bash
curl -s -X POST http://localhost:8090/v1/graphql \
  -H 'Content-Type: application/json' \
  -d '{
    "query": "{ Aggregate { YelpReview(groupBy: [\"city\"]) { groupedBy { value } meta { count } } } }"
  }' | jq '.data.Aggregate.YelpReview | sort_by(-.meta.count) | .[:20] | .[] | {city: .groupedBy.value, reviews: .meta.count}'
```

## Querying the Menu Store

With the recent pipeline updates, menu items are indexed into the `RestaurantMenu` collection. You can query them similarly. For instance, to find menu items related to "spicy":

```bash
curl -s -X POST http://localhost:8090/v1/graphql \
  -H 'Content-Type: application/json' \
  -d '{
    "query": "{ Get { RestaurantMenu(nearText: {concepts: [\"spicy\"]}, limit: 10) { restaurantName dishName description address phoneNumber _additional { certainty distance } } } }"
  }' | jq .
```
