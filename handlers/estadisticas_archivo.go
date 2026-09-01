package handlers

import (
	"encoding/json"
	"os"
	"sync"
)

type Estadisticas struct {
	TotalSocios     int `json:"totalSocios"`
	Activos         int `json:"activos"`
	Inactivos       int `json:"inactivos"`
	Suspendidos     int `json:"suspendidos"`
	TotalAdherentes int `json:"totalAdherentes"`
}

var (
	statsMu   sync.Mutex
	statsPath = "/root/backendauren/estadisticas.json" // después ajustamos la ruta
)

func leerStats() (Estadisticas, error) {
	statsMu.Lock()
	defer statsMu.Unlock()

	data, err := os.ReadFile(statsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return Estadisticas{}, nil
		}
		return Estadisticas{}, err
	}

	var stats Estadisticas
	if err := json.Unmarshal(data, &stats); err != nil {
		return Estadisticas{}, err
	}
	return stats, nil
}

func guardarStats(stats Estadisticas) error {
	statsMu.Lock()
	defer statsMu.Unlock()

	data, err := json.MarshalIndent(stats, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(statsPath, data, 0644)
}
