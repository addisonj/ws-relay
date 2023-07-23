package main

import (
	"encoding/json"
	"github.com/oklog/ulid/v2"
	"go.uber.org/zap"
	"net/http"
	"os"

	"github.com/olahol/melody"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type tokenResponse struct {
	Token string `json:"token"`
}

func main() {
	logger := zap.Must(zap.NewProduction()).Sugar()
	defer logger.Sync()
	if os.Getenv("APP_ENV") == "development" {
		logger = zap.Must(zap.NewDevelopment()).Sugar()
	}

	relay := NewWSRelay(logger)

	// POST /session
	http.HandleFunc("/session", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
			return
		}

		token := generateToken() // Replace with your own token generation function
		relay.RegisterToken(token)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(tokenResponse{Token: token})
	})

	// GET /session/receive/:token
	http.HandleFunc("/session/receive/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
			return
		}

		token := r.URL.Path[len("/session/receive/"):]
		err := relay.Melody.HandleRequestWithKeys(w, r, map[string]interface{}{
			"token": token,
		})

		if err != nil {
			http.Error(w, "Error handling request", http.StatusInternalServerError)
			logger.Errorw("Error handling request", "error", err)
		}
	})

	// POST /session/send/:token
	http.HandleFunc("/session/send/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
			return
		}

		token := r.URL.Path[len("/session/send/"):]
		err := relay.SendData(token, r.Body)

		if err != nil {
			http.Error(w, "Error sending data", http.StatusInternalServerError)
			logger.Errorw("Error sending data", "error", err)
			return
		}

		w.WriteHeader(http.StatusOK)
	})

	// Prometheus metrics
	http.Handle("/metrics", promhttp.Handler())

	// Websocket handlers
	relay.Melody.HandleConnect(func(s *melody.Session) {
		token, ok := s.Keys["token"]
		if !ok {
			logger.Warn("No token provided on connect")
			s.Close()
			return
		}

		err := relay.RegisterSession(token.(string), s)
		if err != nil {
			logger.Errorw("Error registering session", "error", err)
			s.Close()
			return
		}
	})

	relay.Melody.HandleDisconnect(func(s *melody.Session) {
		token, ok := s.Keys["token"]
		if !ok {
			return
		}

		relay.DisposeToken(token.(string))
	})

	logger.Infow("Started server", "port", "8080")
	http.ListenAndServe(":8080", nil)
}

func generateToken() string {
	return ulid.Make().String()
}
