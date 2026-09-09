package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"cloud.google.com/go/firestore"
)

// normalizarDireccion normaliza una dirección para clave de cache/doc:
// minúsculas, sin acentos, espacios colapsados.
func normalizarDireccion(s string) string {
	s = strings.ToLower(s)
	replacer := strings.NewReplacer(
		"á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u",
	)
	s = replacer.Replace(s)
	fields := strings.Fields(s)
	return strings.Join(fields, " ")
}

// --- Guardar Dirección ---

type guardarDireccionRequest struct {
	Direccion string `json:"direccion"`
	Ciudad    string `json:"ciudad"`
	Provincia string `json:"provincia"`
	PlaceID   string `json:"place_id,omitempty"`
}

type guardarDireccionResponse struct {
	ID               string  `json:"id"`
	Lat              float64 `json:"lat"`
	Lng              float64 `json:"lng"`
	FormattedAddress string  `json:"formatted_address"`
	CacheHit         bool    `json:"cache_hit"`
}

func GuardarDireccion(fsClient *firestore.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		_, err := verifyIDToken(r, AuthClient)
		if err != nil {
			http.Error(w, "no autorizado", http.StatusUnauthorized)
			return
		}

		var input guardarDireccionRequest
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "JSON inválido", http.StatusBadRequest)
			return
		}

		if input.Direccion == "" {
			http.Error(w, "falta dirección", http.StatusBadRequest)
			return
		}

		parts := []string{input.Direccion}
		if input.Ciudad != "" {
			parts = append(parts, input.Ciudad)
		}
		if input.Provincia != "" {
			parts = append(parts, input.Provincia)
		}
		direccionCompleta := strings.Join(parts, ", ")
		norm := normalizarDireccion(direccionCompleta)

		// Buscar en PG geo_cache
		if data, ok := PGCacheGet(norm); ok {
			lat, _ := data["lat"].(float64)
			lng, _ := data["lng"].(float64)
			formattedAddr, _ := data["direccion"].(string)
			if formattedAddr == "" {
				formattedAddr = direccionCompleta
			}

			geoCache.Store(norm, []float64{lat, lng})

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(guardarDireccionResponse{
				ID:               norm,
				Lat:              lat,
				Lng:              lng,
				FormattedAddress: formattedAddr,
				CacheHit:         true,
			})
			return
		}

		lat, lng, errGeo := geocodificarDireccion(direccionCompleta)
		if errGeo != nil {
			http.Error(w, "no se pudo geocodificar: "+errGeo.Error(), http.StatusUnprocessableEntity)
			return
		}

		geoAPICallsMu.Lock()
		direccionesGuardadas++
		geoAPICallsMu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(guardarDireccionResponse{
			ID:               norm,
			Lat:              lat,
			Lng:              lng,
			FormattedAddress: direccionCompleta,
			CacheHit:         false,
		})
	}
}

// --- Buscar Direcciones (autocomplete) ---

type placePrediction struct {
	Description string `json:"description"`
	PlaceID     string `json:"place_id"`
}

type placeAutocompleteResponse struct {
	Predictions []placePrediction `json:"predictions"`
	Status      string            `json:"status"`
}

type buscarDireccionResult struct {
	ID          string  `json:"id,omitempty"`
	Description string  `json:"description"`
	Lat         float64 `json:"lat,omitempty"`
	Lng         float64 `json:"lng,omitempty"`
	PlaceID     string  `json:"place_id,omitempty"`
	Fuente      string  `json:"fuente"`
}

func BuscarDirecciones(fsClient *firestore.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		_, err := verifyIDToken(r, AuthClient)
		if err != nil {
			http.Error(w, "no autorizado", http.StatusUnauthorized)
			return
		}

		q := strings.TrimSpace(r.URL.Query().Get("q"))
		if q == "" {
			http.Error(w, "falta parámetro q", http.StatusBadRequest)
			return
		}
		ciudad := strings.TrimSpace(r.URL.Query().Get("ciudad"))

		normQ := normalizarDireccion(q)
		resultados := []buscarDireccionResult{}

		// 1. Buscar en PG geo_cache (prefix match)
		if PGPool != nil {
			ctx := context.Background()
			rows, err := PGPool.Query(ctx,
				`SELECT cache_key, payload FROM geo_cache
				 WHERE cache_key LIKE $1 || '%'
				   AND tipo = 'geocoding'
				 LIMIT 5`, normQ,
			)
			if err == nil {
				defer rows.Close()
				for rows.Next() {
					var cacheKey string
					var payload []byte
					if err := rows.Scan(&cacheKey, &payload); err != nil {
						continue
					}
					var data map[string]interface{}
					if err := json.Unmarshal(payload, &data); err != nil {
						continue
					}
					lat, _ := data["lat"].(float64)
					lng, _ := data["lng"].(float64)
					desc, _ := data["direccion"].(string)
					if desc == "" {
						desc = cacheKey
					}

					resultados = append(resultados, buscarDireccionResult{
						ID:          cacheKey,
						Description: desc,
						Lat:         lat,
						Lng:         lng,
						Fuente:      "pg",
					})
				}
			}
		}

		var lat, lng float64
		radio := 0
		if ciudad != "" {
			if info, errCiudad := coordenadasCiudad(ciudad); errCiudad == nil {
				lat, lng, radio = info.Lat, info.Lng, info.Radio
			}
		}

		// 2. Si no hay resultados locales, buscar en cache de place autocomplete
		//    y si no, en Google.
		if len(resultados) == 0 {
			// Los resultados del autocomplete dependen de la ciudad (strictbounds),
			// así que la clave caché es q + ciudad.
			consulta := q
			if ciudad != "" {
				consulta = q + ", " + ciudad
			}
			cacheKey := "place_auto:" + normalizarDireccion(consulta)

			if data, ok := PGCacheGet(cacheKey); ok {
				if itemsRaw, exists := data["items"].([]interface{}); exists {
					for _, raw := range itemsRaw {
						if m, ok := raw.(map[string]interface{}); ok {
							item := buscarDireccionResult{Fuente: "cache"}
							if v, ok := m["description"].(string); ok {
								item.Description = v
							}
							if v, ok := m["place_id"].(string); ok {
								item.PlaceID = v
							}
							resultados = append(resultados, item)
						}
					}
				}
			}

			if len(resultados) == 0 {
				apiKey := os.Getenv("GOOGLE_PLACES_API_KEY")
				if apiKey == "" {
					apiKey = os.Getenv("GOOGLE_API_KEY")
				}
				if apiKey != "" {
					googleResults := buscarEnGoogle(q, ciudad, lat, lng, radio, apiKey)
					resultados = append(resultados, googleResults...)
				}
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resultados)
	}
}

func buscarEnGoogle(input, ciudad string, lat, lng float64, radio int, apiKey string) []buscarDireccionResult {
	consulta := input
	if ciudad != "" {
		consulta = input + ", " + ciudad
	}

	endpoint := fmt.Sprintf(
		"https://maps.googleapis.com/maps/api/place/autocomplete/json?input=%s&key=%s&components=country:AR&language=es",
		url.QueryEscape(consulta),
		apiKey,
	)

	if radio > 0 {
		endpoint += fmt.Sprintf("&location=%.6f,%.6f&radius=%d&strictbounds=true", lat, lng, radio)
	}

	resp, err := geoHTTPClient.Get(endpoint)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil
	}

	var acr placeAutocompleteResponse
	if err := json.Unmarshal(body, &acr); err != nil {
		return nil
	}

	if acr.Status != "OK" {
		return nil
	}

	geoAPICallsMu.Lock()
	autocompleteAPICalls++
	geoAPICallsMu.Unlock()

	resultados := make([]buscarDireccionResult, 0, len(acr.Predictions))
	for _, p := range acr.Predictions {
		resultados = append(resultados, buscarDireccionResult{
			Description: p.Description,
			PlaceID:     p.PlaceID,
			Fuente:      "google",
		})
	}

	// Cacheamos las predicciones exitosas en PG geo_cache para no repetir
	// llamadas pagadas al autocomplete de Places.
	if len(resultados) > 0 {
		persistItems := make([]map[string]interface{}, 0, len(resultados))
		for _, it := range resultados {
			persistItems = append(persistItems, map[string]interface{}{
				"description": it.Description,
				"place_id":    it.PlaceID,
			})
		}
		PGCacheSet("place_auto:"+normalizarDireccion(consulta), "place_autocomplete", map[string]interface{}{
			"items": persistItems,
		})
	}

	return resultados
}
