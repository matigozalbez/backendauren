package handlers

import (
	"context"
	"encoding/json"
	"log"
	"time"
)

// PGCacheGet busca un resultado en geo_cache por clave normalizada.
// Devuelve el payload deserializado o nil si no existe.
func PGCacheGet(cacheKey string) (map[string]interface{}, bool) {
	if PGPool == nil {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var payload []byte
	err := PGPool.QueryRow(ctx,
		"SELECT payload FROM geo_cache WHERE cache_key = $1", cacheKey,
	).Scan(&payload)
	if err != nil {
		return nil, false
	}

	var data map[string]interface{}
	if err := json.Unmarshal(payload, &data); err != nil {
		return nil, false
	}
	return data, true
}

// PGCacheSet guarda un resultado en geo_cache (upsert).
func PGCacheSet(cacheKey, tipo string, data interface{}) {
	if PGPool == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		payload, err := json.Marshal(data)
		if err != nil {
			log.Printf("[GEO_CACHE] error marshaling: %v", err)
			return
		}

		_, err = PGPool.Exec(ctx,
			`INSERT INTO geo_cache (cache_key, tipo, payload)
			 VALUES ($1, $2, $3)
			 ON CONFLICT (cache_key) DO UPDATE SET payload = $3`,
			cacheKey, tipo, payload,
		)
		if err != nil {
			log.Printf("[GEO_CACHE] error insertando: %v", err)
		}
	}()
}
