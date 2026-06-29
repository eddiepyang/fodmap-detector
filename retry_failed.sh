#!/bin/bash
docker exec -e PGPASSWORD=fodmap fodmap-detector-postgres-1 psql -U fodmap -d fodmap -t -c "SELECT camis FROM restaurants WHERE status = 'failed_scrape';" | tr -d ' ' | grep -v '^$' > failed_camis.txt

count=$(wc -l < failed_camis.txt)
echo "Found $count failed scrapes. Retrying..."

for camis in $(cat failed_camis.txt); do
  echo "Retrying $camis..."
  go run . restaurants retry $camis --postgres-dsn "postgres://fodmap:fodmap@localhost:5432/fodmap"
done
