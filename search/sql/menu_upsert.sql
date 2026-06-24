INSERT INTO restaurant_menu (
    menu_item_id, business_id, menu_section, restaurant_name,
    city, state, dish_name, description, stated_ingredients,
    has_full_ingredients, source_url, scraped_at, embedding, payload
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
ON CONFLICT (menu_item_id) DO UPDATE SET
    menu_section        = EXCLUDED.menu_section,
    restaurant_name     = EXCLUDED.restaurant_name,
    city                = EXCLUDED.city,
    state               = EXCLUDED.state,
    dish_name           = EXCLUDED.dish_name,
    description         = EXCLUDED.description,
    stated_ingredients  = EXCLUDED.stated_ingredients,
    has_full_ingredients = EXCLUDED.has_full_ingredients,
    source_url          = EXCLUDED.source_url,
    scraped_at          = EXCLUDED.scraped_at,
    embedding           = EXCLUDED.embedding,
    payload             = EXCLUDED.payload
