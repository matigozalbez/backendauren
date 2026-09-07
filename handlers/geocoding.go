package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"
	"unicode"
)

var geoHTTPClient = &http.Client{Timeout: 8 * time.Second}

type geocodeResponse struct {
	Results []struct {
		Geometry struct {
			Location struct {
				Lat float64 `json:"lat"`
				Lng float64 `json:"lng"`
			} `json:"location"`
			Viewport struct {
				Northeast struct {
					Lat float64 `json:"lat"`
					Lng float64 `json:"lng"`
				} `json:"northeast"`
				Southwest struct {
					Lat float64 `json:"lat"`
					Lng float64 `json:"lng"`
				} `json:"southwest"`
			} `json:"viewport"`
		} `json:"geometry"`
		Types []string `json:"types"`
		AddressComponents []struct {
			LongName  string   `json:"long_name"`
			ShortName string   `json:"short_name"`
			Types     []string `json:"types"`
		} `json:"address_components"`
	} `json:"results"`
	Status       string `json:"status"`
	ErrorMessage string `json:"error_message"`
}

// geoCache guarda solo resultados exitosos para no repetir llamadas a la API.
var geoCache sync.Map

// Contadores de uso real de la API de Geocoding (no incluyen hits de cache).
var (
	geoAPICalls          int64
	geoAPICallsMu        sync.Mutex
	geoLastRequest       time.Time
	geoCodingProcessed   int64
	direccionesGuardadas int64
	autocompleteAPICalls int64
)

func GeocodingStatsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(GeoAPIStats())
}

// GeoAPIStats devuelve las estadísticas de uso real de la API de Geocoding.
func GeoAPIStats() map[string]interface{} {
	geoAPICallsMu.Lock()
	defer geoAPICallsMu.Unlock()
	return map[string]interface{}{
		"api_calls_total":        geoAPICalls,
		"processed":              geoCodingProcessed,
		"last_request_at":        geoLastRequest.Format(time.RFC3339),
		"direcciones_guardadas":  direccionesGuardadas,
		"autocomplete_api_calls": autocompleteAPICalls,
	}
}

func geocodificarDireccion(direccionCompleta string) (lat float64, lng float64, err error) {
	geoAPICallsMu.Lock()
	geoCodingProcessed++
	geoAPICallsMu.Unlock()

	clave := normalizarDireccion(direccionCompleta)

	// L1: cache en memoria
	if val, ok := geoCache.Load(clave); ok {
		coords := val.([]float64)
		return coords[0], coords[1], nil
	}

	// L2: Firestore
	if FirestoreClient != nil {
		ctx := context.Background()
		docSnap, errGet := FirestoreClient.Collection("direcciones").Doc(clave).Get(ctx)
		if errGet == nil && docSnap.Exists() {
			data := docSnap.Data()
			latFS, okLat := data["lat"].(float64)
			lngFS, okLng := data["lng"].(float64)
			if okLat && okLng {
				coords := []float64{latFS, lngFS}
				geoCache.Store(clave, coords)
				return latFS, lngFS, nil
			}
		}
	}

	apiKey := os.Getenv("GOOGLE_API_KEY")
	if apiKey == "" {
		return 0, 0, fmt.Errorf("GOOGLE_API_KEY no configurada")
	}

	endpoint := fmt.Sprintf(
		"https://maps.googleapis.com/maps/api/geocode/json?address=%s&key=%s&components=country:AR",
		url.QueryEscape(direccionCompleta),
		apiKey,
	)

	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, 0, err
	}

	geoAPICallsMu.Lock()
	geoAPICalls++
	geoLastRequest = time.Now()
	geoAPICallsMu.Unlock()

	horaInicio := time.Now()
	resp, err := geoHTTPClient.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, 0, fmt.Errorf("google geocoding respondió %s", resp.Status)
	}
	duracion := time.Since(horaInicio)
	fmt.Printf("[GEOCODING] llamada real a API #%d | %s | duración: %s\n",
		geoAPICalls, direccionCompleta, duracion)

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, 0, err
	}

	var gr geocodeResponse
	if err := json.Unmarshal(body, &gr); err != nil {
		return 0, 0, err
	}

	if gr.Status != "OK" || len(gr.Results) == 0 {
		return 0, 0, fmt.Errorf(
			"no se pudo geocodificar (status: %s, error: %s)",
			gr.Status,
			gr.ErrorMessage,
		)
	}

	loc := gr.Results[0].Geometry.Location
	geoCache.Store(clave, []float64{loc.Lat, loc.Lng})

	// Persistimos en Firestore para la próxima vez (best effort).
	go func() {
		defer func() { _ = recover() }()
		if FirestoreClient == nil {
			return
		}
		ctx := context.Background()
		docRef := FirestoreClient.Collection("direcciones").Doc(clave)
		docSnap, errGet := docRef.Get(ctx)
		if errGet == nil && docSnap.Exists() {
			return
		}
		_, errSet := docRef.Set(ctx, map[string]interface{}{
			"direccion": direccionCompleta,
			"lat":       loc.Lat,
			"lng":       loc.Lng,
		})
		if errSet == nil {
			geoAPICallsMu.Lock()
			direccionesGuardadas++
			geoAPICallsMu.Unlock()
		}
	}()

	return loc.Lat, loc.Lng, nil
}

// ciudadInfo guarda centro y radio de una ciudad para acotar el autocomplete.
type ciudadInfo struct {
	Lat   float64
	Lng   float64
	Radio int
}

// ciudadCacheEntry admite cache en memoria con cache negativo (ok=false).
type ciudadCacheEntry struct {
	info ciudadInfo
	ok   bool
	ts   time.Time
}

// ciudadesCache guarda solo en memoria para no escribir en Firestore.
var ciudadesCache sync.Map

// coordenadasCiudad devuelve centro y radio de una ciudad usando solo memoria.
// Si la ciudad no existe, se recuerda 1h para no repetir llamadas.
func coordenadasCiudad(ciudad string) (ciudadInfo, error) {
	clave := normalizarDireccion(ciudad)
	if clave == "" || len(clave) < 3 || !esCiudadValida(clave) {
		return ciudadInfo{}, fmt.Errorf("ciudad inválida")
	}

	if val, ok := ciudadesCache.Load(clave); ok {
		entry := val.(ciudadCacheEntry)
		if entry.ok {
			return entry.info, nil
		}
		if time.Since(entry.ts) < time.Hour {
			return ciudadInfo{}, fmt.Errorf("ciudad no ubicada recientemente")
		}
	}

	info, err := geocodificarCiudad(ciudad, clave)
	if err != nil {
		ciudadesCache.Store(clave, ciudadCacheEntry{ts: time.Now()})
		return ciudadInfo{}, err
	}
	return info, nil
}

// esCiudadValida acepta solo letras y espacios.
func esCiudadValida(clave string) bool {
	for _, r := range clave {
		if unicode.IsLetter(r) || r == ' ' {
			continue
		}
		return false
	}
	return true
}

// geocodificarCiudad resuelve el centro de la ciudad, eligiendo un resultado de
// tipo "locality" (no la provincia). No escribe en Firestore.
func geocodificarCiudad(ciudad, clave string) (ciudadInfo, error) {
	apiKey := os.Getenv("GOOGLE_API_KEY")
	if apiKey == "" {
		return ciudadInfo{}, fmt.Errorf("GOOGLE_API_KEY no configurada")
	}

	endpoint := fmt.Sprintf(
		"https://maps.googleapis.com/maps/api/geocode/json?address=%s&key=%s&components=country:AR&language=es",
		url.QueryEscape(ciudad),
		apiKey,
	)

	resp, err := geoHTTPClient.Get(endpoint)
	if err != nil {
		return ciudadInfo{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ciudadInfo{}, fmt.Errorf("geocoding de ciudad respondió %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return ciudadInfo{}, err
	}

	var gr geocodeResponse
	if err := json.Unmarshal(body, &gr); err != nil {
		return ciudadInfo{}, err
	}

	if gr.Status != "OK" || len(gr.Results) == 0 {
		return ciudadInfo{}, fmt.Errorf("no se pudo ubicar la ciudad (status: %s)", gr.Status)
	}

	geoAPICallsMu.Lock()
	geoAPICalls++
	geoLastRequest = time.Now()
	geoAPICallsMu.Unlock()

	resultado := gr.Results[0]
	conLocalidad := false
	for _, r := range gr.Results {
		for _, t := range r.Types {
			if t == "locality" {
				resultado = r
				conLocalidad = true
				break
			}
		}
		if conLocalidad {
			break
		}
	}

	loc := resultado.Geometry.Location
	vp := resultado.Geometry.Viewport
	radio := radioCiudadDesdeViewport(
		loc.Lat, loc.Lng,
		vp.Northeast.Lat, vp.Northeast.Lng,
		vp.Southwest.Lat, vp.Southwest.Lng,
	)

	info := ciudadInfo{Lat: loc.Lat, Lng: loc.Lng, Radio: radio}
	ciudadesCache.Store(clave, ciudadCacheEntry{info: info, ok: true, ts: time.Now()})

	return info, nil
}

// radioCiudadDesdeViewport calcula un radio que abarca el viewport de la ciudad.
func radioCiudadDesdeViewport(lat, lng, neLat, neLng, swLat, swLng float64) int {
	if neLat == 0 && neLng == 0 && swLat == 0 && swLng == 0 {
		return 25000
	}

	esquinas := [][2]float64{
		{neLat, neLng},
		{swLat, swLng},
		{neLat, swLng},
		{swLat, neLng},
	}
	radio := 0.0
	for _, e := range esquinas {
		d := distanciaKm(lat, lng, e[0], e[1]) * 1000
		if d > radio {
			radio = d
		}
	}
	radio += 5000
	if radio < 5000 {
		radio = 5000
	}
	if radio > 50000 {
		radio = 50000
	}
	return int(radio)
}

// distanciaKm devuelve la distancia en kilómetros entre dos coordenadas (Haversine).
func distanciaKm(lat1, lng1, lat2, lng2 float64) float64 {
	const radioTierraKm = 6371.0

	lat1Rad := lat1 * math.Pi / 180
	lat2Rad := lat2 * math.Pi / 180
	deltaLat := (lat2 - lat1) * math.Pi / 180
	deltaLng := (lng2 - lng1) * math.Pi / 180

	a := math.Sin(deltaLat/2)*math.Sin(deltaLat/2) +
		math.Cos(lat1Rad)*math.Cos(lat2Rad)*math.Sin(deltaLng/2)*math.Sin(deltaLng/2)
	a = math.Min(a, 1)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))

	return radioTierraKm * c
}
