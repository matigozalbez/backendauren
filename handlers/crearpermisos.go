package handlers

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
	"io"

	"cloud.google.com/go/firestore"
	"firebase.google.com/go/v4/auth"
)

type CrearAdminInput struct {
	Nombre   string `json:"nombre"`
	Apellido string `json:"apellido"`
	Email    string `json:"email"`
	Rol      string `json:"rol"` // informativo — el permiso real lo da el custom claim
}

func CrearAdmin(fsClient *firestore.Client, authClient *auth.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method != http.MethodPost {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		var input CrearAdminInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "JSON inválido", http.StatusBadRequest)
			return
		}

		input.Nombre = strings.TrimSpace(input.Nombre)
		input.Apellido = strings.TrimSpace(input.Apellido)
		input.Email = strings.TrimSpace(strings.ToLower(input.Email))
		input.Rol = strings.TrimSpace(input.Rol)

		if input.Nombre == "" || input.Apellido == "" || input.Email == "" {
			http.Error(w, "faltan datos (nombre, apellido o email)", http.StatusBadRequest)
			return
		}

		ctx := context.Background()

		// Si ya existe un usuario con ese email, no lo pisamos — lo tratamos como error explícito.
		if _, err := authClient.GetUserByEmail(ctx, input.Email); err == nil {
			http.Error(w, "ya existe un usuario con ese email", http.StatusConflict)
			return
		}

		tempPassword, err := generarPasswordTemporal()
		if err != nil {
			http.Error(w, "error generando credenciales", http.StatusInternalServerError)
			return
		}

		nombreCompleto := strings.TrimSpace(input.Nombre + " " + input.Apellido)

		newUser := (&auth.UserToCreate{}).
			Email(input.Email).
			Password(tempPassword).
			DisplayName(nombreCompleto).
			EmailVerified(false)

		userRecord, err := authClient.CreateUser(ctx, newUser)
		if err != nil {
			http.Error(w, "error creando el usuario: "+err.Error(), http.StatusInternalServerError)
			return
		}

		if err := authClient.SetCustomUserClaims(ctx, userRecord.UID, map[string]interface{}{
			"admin": true,
		}); err != nil {
			http.Error(w, "usuario creado, pero falló al asignarle el permiso de admin", http.StatusInternalServerError)
			return
		}

		_, err = fsClient.Collection("admins").Doc(userRecord.UID).Set(ctx, map[string]interface{}{
			"nombre":   input.Nombre,
			"apellido": input.Apellido,
			"email":    input.Email,
			"rol":      input.Rol,
			"creadoEn": firestore.ServerTimestamp,
		})
		if err != nil {
			http.Error(w, "usuario creado, pero falló al guardar el registro en admins", http.StatusInternalServerError)
			return
		}

		// Link para que el nuevo admin elija su propia contraseña en vez de usar la temporal.
		resetLink, err := authClient.PasswordResetLink(ctx, input.Email)
		if err != nil {
			// No frenamos el flujo por esto — el usuario ya quedó creado y funcional.
			fmt.Printf("⚠️ no se pudo generar el link de reset para %s: %v\n", input.Email, err)
		} else if err := enviarEmailBienvenidaAdmin(input.Email, input.Nombre, resetLink); err != nil {
			fmt.Printf("⚠️ no se pudo enviar el mail de bienvenida a %s: %v\n", input.Email, err)
		}

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{
			"status": "ok",
			"uid":    userRecord.UID,
			"email":  input.Email,
		})
	}
}

func generarPasswordTemporal() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Reemplazá esto por tu helper de Resend ya existente si lo tenés (el que usa CrearPassword/SolicitarCodigo).
func enviarEmailBienvenidaAdmin(to, nombre, resetLink string) error {
	fmt.Printf("\n=== [RESEND LOG] Iniciando envío de mail ===\n")
	fmt.Printf("-> Destinatario (to): %s\n", to)
	fmt.Printf("-> Nombre: %s\n", nombre)
	fmt.Printf("-> ResetLink: %s\n", resetLink)
	fmt.Printf("-> Longitud RESEND_API_KEY: %d caracteres\n", len(RESEND_API_KEY))

	html := fmt.Sprintf(`
		<p>Hola %s,</p>
		<p>Se creó una cuenta de administrador para vos en el Panel Admin de Auren.</p>
		<p><a href="%s">Hacé clic acá para elegir tu contraseña</a> y después ingresá con tu email.</p>
	`, nombre, resetLink)

payload := map[string]interface{}{
    "from":    "Auren <admin@formulariosalud.com.ar>",
    "to":      []string{to},
    "subject": "Te dieron acceso al Panel Admin de Auren",
    "html":    html,
}

	body, err := json.Marshal(payload)
	if err != nil {
		fmt.Printf("❌ [RESEND LOG] Error haciendo Marshal del JSON: %v\n", err)
		return err
	}
	fmt.Printf("-> Body JSON preparado: %s\n", string(body))

	req, err := http.NewRequest("POST", "https://api.resend.com/emails", bytes.NewBuffer(body))
	if err != nil {
		fmt.Printf("❌ [RESEND LOG] Error creando http.NewRequest: %v\n", err)
		return err
	}
	req.Header.Set("Authorization", "Bearer "+RESEND_API_KEY)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	fmt.Printf("-> Enviando HTTP POST a Resend...\n")
	
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("❌ [RESEND LOG] Error en la petición de red (client.Do): %v\n", err)
		return err
	}
	defer resp.Body.Close()

	// Leemos la respuesta completa del cuerpo devuelto por Resend
	respBodyBytes, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		fmt.Printf("⚠️ [RESEND LOG] No se pudo leer el body de la respuesta: %v\n", readErr)
	}
	respBodyStr := string(respBodyBytes)

	fmt.Printf("-> HTTP Status Code: %d\n", resp.StatusCode)
	fmt.Printf("-> Resend Response Body: %s\n", respBodyStr)

	if resp.StatusCode >= 300 {
		fmt.Printf("❌ [RESEND LOG] Falló el envío. Status >= 300\n")
		return fmt.Errorf("resend devolvió status %d: %s", resp.StatusCode, respBodyStr)
	}

	fmt.Printf("✅ [RESEND LOG] Mail enviado con éxito!\n===========================================\n\n")
	return nil
}