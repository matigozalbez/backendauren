package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

var geoHTTPClient = &http.Client{Timeout: 8 * time.Second}

type geocodeResponse struct {
	Results []struct {
		Geometry struct {
			Location struct {
				Lat float64 `json:"lat"`
				Lng float64 `json:"lng"`
			} `json:"location"`
		} `json:"geometry"`
	} `json:"results"`
	Status       string `json:"status"`
	ErrorMessage string `json:"error_message"`
}

// geoCache guarda solo resultados exitosos para no repetir llamadas a la API.
var geoCache sync.Map

func geocodificarDireccion(direccionCompleta string) (lat float64, lng float64, err error) {
	clave := strings.ToLower(strings.Join(strings.Fields(direccionCompleta), " "))
	if val, ok := geoCache.Load(clave); ok {
		coords := val.([]float64)
		return coords[0], coords[1], nil
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

	resp, err := geoHTTPClient.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, 0, fmt.Errorf("google geocoding respondió %s", resp.Status)
	}

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
	return loc.Lat, loc.Lng, nil
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