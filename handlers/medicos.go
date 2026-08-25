package handlers


import (
	"context"
	"encoding/json"
	"net/http"

	"cloud.google.com/go/firestore"
)

type MedicoInput struct {
	Nombre   		string `json:"nombre"`
	Apellido 		string `json:"apellido"`
	DNI 			string `json:"dni"`
	Ciudad   		string `json:"ciudad"`
	Provincia 		string `json:"provincia"`
	Especialidad 	string `json:"especialidad"`
	Direccion		string `json:"direccion"`
	Imagen       string `json:"imagen"`
}



func CrearMedico(fsClient *firestore.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		var input MedicoInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "JSON inválido", http.StatusBadRequest)
			return
		}

		if input.Nombre == "" || input.Apellido == "" || input.DNI == "" || input.Ciudad == "" || input.Especialidad == "" || input.Direccion == "" {
			http.Error(w, "faltan datos", http.StatusBadRequest)
			return
		}



		ctx := context.Background()
		_, err := fsClient.Collection("medicos").Doc(input.DNI).Set(ctx, map[string]interface{}{
			"nombre":                input.Nombre,
			"apellido":              input.Apellido,
			"dni":                   input.DNI,
			"ciudad":             	 input.Ciudad,
			"provincia":             input.Provincia,
			"especialidad":			 input.Especialidad,
			"direccion":             input.Direccion,
			"imagen":             	 input.Imagen,
			"uid":                   nil,
		})
		if err != nil {
			http.Error(w, "error guardando medico", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}