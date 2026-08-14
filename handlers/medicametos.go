package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

type Medicamento struct {
	Codigo       string  `json:"codigo"` // viene de TROQUEL, identificador único del producto
	Nombre       string  `json:"nombre"`
	Droga        string  `json:"droga"`
	Laboratorio  string  `json:"laboratorio"`
	Presentacion string  `json:"presentacion"`
	Forma        string  `json:"forma"`
	Precio       float64 `json:"precio"`
}

// Exportada (mayúscula) y el NOMBRE tiene que ser exactamente el que llamás
// desde tu main: handlers.Medicamentos(w, r) — ajustá el registro en main.go
// para que diga "Medicamentos" y no "Medicamento"
func Medicamentos(w http.ResponseWriter, r *http.Request) {

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Metodo no permitido", http.StatusMethodNotAllowed)
		return
	}

	var request struct {
		Searchdata string `json:"searchdata"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "JSON invalido", http.StatusBadRequest)
		return
	}

	payload := map[string]string{"searchdata": request.Searchdata}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		http.Error(w, "Error creando request", 500)
		return
	}

	req, err := http.NewRequest(
		http.MethodPost,
		"https://cnpm.msal.gov.ar/api/vademecum",
		bytes.NewBuffer(jsonData),
	)
	if err != nil {
		http.Error(w, "Error creando consulta", 500)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "Error consultando CNPM", 500)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, "Error leyendo respuesta", 500)
		return
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("CNPM devolvió status %d: %s", resp.StatusCode, string(body))
		http.Error(w, "Error consultando CNPM", http.StatusBadGateway)
		return
	}

	var data []map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		http.Error(w, "Respuesta invalida del vademecum", 500)
		return
	}

	medicamentos := make([]Medicamento, 0)
	for _, item := range data {
		medicamentos = append(medicamentos, Medicamento{
			Codigo:       getString(item["TROQUEL"]),
			Nombre:       getString(item["NOMBRE"]),
			Droga:        getString(item["DROGA"]),
			Laboratorio:  getString(item["LABORATORIO"]),
			Presentacion: getString(item["PRESENTACION"]),
			Forma:        getString(item["FORMA"]),
			Precio:       getFloat(item["PRECIO"]),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(medicamentos)
}

func getString(value interface{}) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func getFloat(value interface{}) float64 {
	if value == nil {
		return 0
	}
	// json.Unmarshal a interface{} siempre decodifica números como float64
	if f, ok := value.(float64); ok {
		return f
	}
	return 0
}
