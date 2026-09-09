package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// ciudadSugerencia es una ciudad candidata para el autocomplete de ciudad.
type ciudadSugerencia struct {
	Nombre      string  `json:"nombre"`
	Description string  `json:"description"`
	Lat         float64 `json:"lat,omitempty"`
	Lng         float64 `json:"lng,omitempty"`
	Fuente      string  `json:"fuente"`
}

type ciudadesSugerenciaEntry struct {
	items []ciudadSugerencia
	ok    bool
	ts    time.Time
}

// ciudadesSugerenciasCache mantiene solo en memoria (0 writes a Firestore).
var ciudadesSugerenciasCache sync.Map

// BuscarCiudades devuelve ciudades candidatas al tipear el nombre.
// Nunca escribe en Firestore.
func BuscarCiudades() http.HandlerFunc {
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
		clave := normalizarDireccion(q)
		if clave == "" || len(clave) < 3 || !esCiudadValida(clave) {
			writeCiudadesResult(w, nil)
			return
		}

		if val, ok := ciudadesSugerenciasCache.Load(clave); ok {
			entry := val.(ciudadesSugerenciaEntry)
			if entry.ok {
				writeCiudadesResult(w, entry.items)
				return
			}
			if time.Since(entry.ts) < time.Hour {
				writeCiudadesResult(w, nil)
				return
			}
		}

		// L2: PostgreSQL geo_cache
		if data, ok := PGCacheGet("ciudad_auto:" + clave); ok {
			if itemsRaw, exists := data["items"].([]interface{}); exists {
				items := make([]ciudadSugerencia, 0, len(itemsRaw))
				for _, raw := range itemsRaw {
					if m, ok := raw.(map[string]interface{}); ok {
						item := ciudadSugerencia{Fuente: "ciudad"}
						if v, ok := m["nombre"].(string); ok {
							item.Nombre = v
						}
						if v, ok := m["description"].(string); ok {
							item.Description = v
						}
						if v, ok := m["lat"].(float64); ok {
							item.Lat = v
						}
						if v, ok := m["lng"].(float64); ok {
							item.Lng = v
						}
						items = append(items, item)
					}
				}
				ciudadesSugerenciasCache.Store(clave, ciudadesSugerenciaEntry{
					items: items,
					ok:    true,
					ts:    time.Now(),
				})
				writeCiudadesResult(w, items)
				return
			}
		}

		items, valida := buscarCiudadesGoogle(q)
		if valida && len(items) > 0 {
			// Persistir en PG geo_cache
			persistItems := make([]map[string]interface{}, 0, len(items))
			for _, it := range items {
				persistItems = append(persistItems, map[string]interface{}{
					"nombre":      it.Nombre,
					"description": it.Description,
					"lat":         it.Lat,
					"lng":         it.Lng,
				})
			}
			PGCacheSet("ciudad_auto:"+clave, "ciudad_autocomplete", map[string]interface{}{
				"items": persistItems,
			})
		}
		ciudadesSugerenciasCache.Store(clave, ciudadesSugerenciaEntry{
			items: items,
			ok:    valida && len(items) > 0,
			ts:    time.Now(),
		})
		writeCiudadesResult(w, items)
	}
}

func writeCiudadesResult(w http.ResponseWriter, items []ciudadSugerencia) {
	if items == nil {
		items = []ciudadSugerencia{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(items)
}

func buscarCiudadesGoogle(q string) ([]ciudadSugerencia, bool) {
	if os.Getenv("GOOGLE_API_KEY") == "" {
		return nil, false
	}

	endpoint := fmt.Sprintf(
		"https://maps.googleapis.com/maps/api/geocode/json?address=%s&key=%s&components=country:AR&language=es",
		url.QueryEscape(q),
		os.Getenv("GOOGLE_API_KEY"),
	)

	resp, err := geoHTTPClient.Get(endpoint)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, false
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, false
	}

	var gr geocodeResponse
	if err := json.Unmarshal(body, &gr); err != nil {
		return nil, false
	}

	if gr.Status != "OK" {
		return nil, true
	}

	geoAPICallsMu.Lock()
	geoAPICalls++
	geoLastRequest = time.Now()
	geoAPICallsMu.Unlock()

	items := []ciudadSugerencia{}
	for _, res := range gr.Results {
		nombre, provincia := nombreYProvincia(res.AddressComponents)
		if nombre == "" {
			continue
		}
		desc := nombre
		if provincia != "" {
			desc = nombre + ", " + provincia
		}
		items = append(items, ciudadSugerencia{
			Nombre:      nombre,
			Description: desc,
			Lat:         res.Geometry.Location.Lat,
			Lng:         res.Geometry.Location.Lng,
			Fuente:      "ciudad",
		})
		if len(items) >= 6 {
			break
		}
	}

	return items, true
}

// nombreYProvincia extrae nombre (locality/town) y provincia de un resultado.
func nombreYProvincia(components []struct {
	LongName  string   `json:"long_name"`
	ShortName string   `json:"short_name"`
	Types     []string `json:"types"`
}) (nombre, provincia string) {
	for _, comp := range components {
		for _, t := range comp.Types {
			if (t == "locality" || t == "town") && nombre == "" {
				nombre = comp.LongName
			}
			if t == "administrative_area_level_1" {
				provincia = comp.LongName
			}
		}
	}
	return nombre, provincia
}