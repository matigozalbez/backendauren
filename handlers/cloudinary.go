package handlers

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"time"
)

// Configuración de Cloudinary (subida de imágenes de estudios).
// Se inicializa desde main.go con handlers.InicializarCloudinary().
var (
	CLOUDINARY_CLOUD_NAME string
	CLOUDINARY_API_KEY    string
	CLOUDINARY_API_SECRET string
)

func InicializarCloudinary() {
	CLOUDINARY_CLOUD_NAME = os.Getenv("CLOUDINARY_CLOUD_NAME")
	CLOUDINARY_API_KEY = os.Getenv("CLOUDINARY_API_KEY")
	CLOUDINARY_API_SECRET = os.Getenv("CLOUDINARY_API_SECRET")
	if CLOUDINARY_CLOUD_NAME == "" || CLOUDINARY_API_KEY == "" || CLOUDINARY_API_SECRET == "" {
		log.Println("ADVERTENCIA: faltan credenciales Cloudinary (CLOUDINARY_CLOUD_NAME/API_KEY/API_SECRET)")
	}
}

// SubirACloudinary sube una imagen a Cloudinary con firma SHA1 (API server-side)
// guardándola en la carpeta auren_estudios/<uid>. Devuelve la URL pública.
func SubirACloudinary(uid, contentType string, contenido []byte) (string, error) {
	if CLOUDINARY_CLOUD_NAME == "" || CLOUDINARY_API_KEY == "" || CLOUDINARY_API_SECRET == "" {
		return "", fmt.Errorf("credenciales Cloudinary ausentes")
	}

	carpeta := "auren_estudios/" + uid
	timestamp := fmt.Sprintf("%d", time.Now().Unix())

	// Firma SHA1 de los parámetros ordenados alfabéticamente + API secret.
	toSign := "folder=" + carpeta + "&timestamp=" + timestamp + CLOUDINARY_API_SECRET
	h := sha1.New()
	h.Write([]byte(toSign))
	firma := hex.EncodeToString(h.Sum(nil))

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	_ = writer.WriteField("api_key", CLOUDINARY_API_KEY)
	_ = writer.WriteField("timestamp", timestamp)
	_ = writer.WriteField("folder", carpeta)
	_ = writer.WriteField("signature", firma)

	part, err := writer.CreateFormFile("file", "estudio")
	if err != nil {
		return "", err
	}
	if _, err := part.Write(contenido); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}

	req, err := http.NewRequest(
		http.MethodPost,
		fmt.Sprintf("https://api.cloudinary.com/v1_1/%s/image/upload", CLOUDINARY_CLOUD_NAME),
		body,
	)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("cloudinary status %d: %s", resp.StatusCode, string(respBody))
	}

	var resultado struct {
		SecureURL string `json:"secure_url"`
		URL       string `json:"url"`
	}
	if err := json.Unmarshal(respBody, &resultado); err != nil {
		return "", err
	}

	if resultado.SecureURL != "" {
		return resultado.SecureURL, nil
	}
	if resultado.URL != "" {
		return resultado.URL, nil
	}
	return "", fmt.Errorf("cloudinary no devolvió url")
}