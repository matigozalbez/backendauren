package handlers

import (
	"bufio"
	"net/http"
	"os"
)

func LogsServidor(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodGet {
		http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
		return
	}

	file, err := os.Open("logs/app.json.log")
	if err != nil {
		if os.IsNotExist(err) {
			w.Write([]byte("[]"))
			return
		}

		http.Error(w, "error leyendo logs: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer file.Close()

	logs := make([]string, 0)

	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		logs = append(logs, scanner.Text())
	}

	if err := scanner.Err(); err != nil {
		http.Error(w, "error leyendo logs: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Write([]byte("["))

	for i, log := range logs {
		if i > 0 {
			w.Write([]byte(","))
		}

		w.Write([]byte(log))
	}

	w.Write([]byte("]"))
}