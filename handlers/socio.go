package handlers

// handlers/socios.go

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"cloud.google.com/go/firestore"
	"firebase.google.com/go/v4/auth"
)

type AdherenteInput struct {
	Relacion string `json:"relacion"`
	Nombre   string `json:"nombre"`
	Apellido string `json:"apellido"`
	DNI      string `json:"dni"`
	Edad     string `json:"edad"`
}

type SocioInput struct {
	DNI                   string           `json:"dni"`
	Nombre                string           `json:"nombre"`
	Apellido              string           `json:"apellido"`
	Email                 string           `json:"email"`
	Edad                  string           `json:"edad"`
	Provincia             string           `json:"provincia"`
	Ciudad                string           `json:"ciudad"`
	Direccion             string           `json:"direccion"`
	MetodoPago            string           `json:"metodoPago"`
	CBU                   string           `json:"cbu"`
	TarjetaUltimosDigitos string           `json:"tarjetaUltimosDigitos"` // solo los últimos 4, nunca el número completo
	TarjetaVencimiento    string           `json:"tarjetaVencimiento"`
	Planes                []string         `json:"planes"`
	Estado                string           `json:"estado"`
	Adherentes            []AdherenteInput `json:"adherentes"`
}

func verifyIDToken(r *http.Request, authClient *auth.Client) (string, error) {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return "", errors.New("sin token")
	}
	idToken := strings.TrimPrefix(header, "Bearer ")

	token, err := authClient.VerifyIDToken(context.Background(), idToken)
	if err != nil {
		return "", err
	}
	return token.UID, nil
}

func CrearSocio(fsClient *firestore.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		var input SocioInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "JSON inválido", http.StatusBadRequest)
			return
		}

		if input.DNI == "" || input.Nombre == "" || input.Apellido == "" {
			http.Error(w, "faltan datos", http.StatusBadRequest)
			return
		}
		if len(input.Planes) > 4 {
			http.Error(w, "máximo 4 planes por socio", http.StatusBadRequest)
			return
		}
		if len(input.Adherentes) > 10 {
			http.Error(w, "máximo 10 adherentes por titular", http.StatusBadRequest)
			return
		}
		if input.Estado == "" {
			input.Estado = "activo"
		}

		// Convertimos adherentes a []interface{} para que Firestore lo
		// guarde como array de mapas
		adherentesData := make([]interface{}, 0, len(input.Adherentes))
		for _, a := range input.Adherentes {
			adherentesData = append(adherentesData, map[string]interface{}{
				"relacion": a.Relacion,
				"nombre":   a.Nombre,
				"apellido": a.Apellido,
				"dni":      a.DNI,
				"edad":     a.Edad,
			})
		}

		ctx := context.Background()
		_, err := fsClient.Collection("socios").Doc(input.DNI).Set(ctx, map[string]interface{}{
			"dni":                   input.DNI,
			"nombre":                input.Nombre,
			"apellido":              input.Apellido,
			"email":                 input.Email,
			"edad":                  input.Edad,
			"provincia":             input.Provincia,
			"ciudad":                input.Ciudad,
			"direccion":             input.Direccion,
			"metodoPago":            input.MetodoPago,
			"cbu":                   input.CBU,
			"tarjetaUltimosDigitos": input.TarjetaUltimosDigitos,
			"tarjetaVencimiento":    input.TarjetaVencimiento,
			"planes":                input.Planes,
			"estado":                input.Estado,
			"adherentes":            adherentesData,
			"uid":                   nil,
		})
		if err != nil {
			http.Error(w, "error guardando socio", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}

func VincularSocio(fsClient *firestore.Client, authClient *auth.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, err := verifyIDToken(r, authClient)
		if err != nil {
			http.Error(w, "no autorizado, iniciá sesión", http.StatusUnauthorized)
			return
		}

		var input SocioInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "JSON inválido", http.StatusBadRequest)
			return
		}

		ctx := context.Background()
		doc, err := fsClient.Collection("socios").Doc(input.DNI).Get(ctx)
		if err != nil || !doc.Exists() {
			http.Error(w, "ese DNI no pertenece a ningún socio", http.StatusNotFound)
			return
		}

		data := doc.Data()
		if data["uid"] != nil {
			http.Error(w, "este DNI ya tiene una cuenta vinculada", http.StatusConflict)
			return
		}

		mailRegistrado, _ := data["email"].(string)

		usuarioAuth, err := authClient.GetUser(ctx, uid)
		if err != nil {
			http.Error(w, "error verificando cuenta", http.StatusInternalServerError)
			return
		}

		if !strings.EqualFold(strings.TrimSpace(usuarioAuth.Email), strings.TrimSpace(mailRegistrado)) {
			http.Error(w, "el mail de tu cuenta no coincide con el registrado para este DNI", http.StatusForbidden)
			return
		}

		_, err = fsClient.Collection("socios").Doc(input.DNI).Update(ctx, []firestore.Update{
			{Path: "uid", Value: uid},
		})
		if err != nil {
			http.Error(w, "error vinculando cuenta", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}

func VerificarVinculacion(fsClient *firestore.Client, authClient *auth.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, err := verifyIDToken(r, authClient)
		if err != nil {
			http.Error(w, "no autorizado", http.StatusUnauthorized)
			return
		}

		ctx := context.Background()
		iter := fsClient.Collection("socios").Where("uid", "==", uid).Limit(1).Documents(ctx)
		docs, err := iter.GetAll()
		if err != nil {
			http.Error(w, "error consultando", http.StatusInternalServerError)
			return
		}

		vinculado := len(docs) > 0
		json.NewEncoder(w).Encode(map[string]bool{"vinculado": vinculado})
	}
}

func MiSocio(fsClient *firestore.Client, authClient *auth.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, err := verifyIDToken(r, authClient)
		if err != nil {
			http.Error(w, "no autorizado", http.StatusUnauthorized)
			return
		}

		ctx := context.Background()
		iter := fsClient.Collection("socios").Where("uid", "==", uid).Limit(1).Documents(ctx)
		docs, err := iter.GetAll()
		if err != nil || len(docs) == 0 {
			http.Error(w, "socio no encontrado", http.StatusNotFound)
			return
		}

		data := docs[0].Data()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(data)
	}
}
