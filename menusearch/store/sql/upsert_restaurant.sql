INSERT INTO restaurants (
    camis, dba, boro, building, street, zipcode, phone, address, cuisine, latitude, longitude, nta, status
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
) ON CONFLICT (camis) DO UPDATE SET
    dba = EXCLUDED.dba,
    boro = EXCLUDED.boro,
    building = EXCLUDED.building,
    street = EXCLUDED.street,
    zipcode = EXCLUDED.zipcode,
    phone = EXCLUDED.phone,
    address = COALESCE(EXCLUDED.address, restaurants.address),
    cuisine = EXCLUDED.cuisine,
    latitude = EXCLUDED.latitude,
    longitude = EXCLUDED.longitude,
    nta = EXCLUDED.nta
RETURNING id;
