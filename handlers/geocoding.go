package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
)

// --- Nuevo: helper de geocoding ---

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

func geocodificarDireccion(direccionCompleta string) (lat float64, lng float64, err error) {
	apiKey := os.Getenv("GOOGLE_API_KEY")
	if apiKey == "" {
		return 0, 0, fmt.Errorf("GOOGLE_API_KEY no configurada")
	}

	endpoint := fmt.Sprintf(
		"https://maps.googleapis.com/maps/api/geocode/json?address=%s&key=%s",
		url.QueryEscape(direccionCompleta),
		apiKey,
	)

	resp, err := http.Get(endpoint)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
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
	return loc.Lat, loc.Lng, nil
}

// --- Nuevo: distancia entre dos coordenadas (Haversine) ---

func distanciaKm(lat1, lng1, lat2, lng2 float64) float64 {
	const radioTierraKm = 6371.0

	lat1Rad := lat1 * math.Pi / 180
	lat2Rad := lat2 * math.Pi / 180
	deltaLat := (lat2 - lat1) * math.Pi / 180
	deltaLng := (lng2 - lng1) * math.Pi / 180

	a := math.Sin(deltaLat/2)*math.Sin(deltaLat/2) +
		math.Cos(lat1Rad)*math.Cos(lat2Rad)*math.Sin(deltaLng/2)*math.Sin(deltaLng/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))

	return radioTierraKm * c
}
