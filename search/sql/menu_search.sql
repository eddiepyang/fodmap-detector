SELECT
    menu_item_id, business_id, menu_section, restaurant_name,
    city, state, dish_name, description, stated_ingredients,
    has_full_ingredients, source_url, scraped_at, payload
FROM restaurant_menu
ORDER BY embedding <=> $1
LIMIT $2
