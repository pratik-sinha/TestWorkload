package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
)

type SecretConfig struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type DBConfig struct {
	Host    string
	Port    int
	DBName  string
	Secrets SecretConfig
}

type CheckResponse struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	Details any    `json:"details,omitempty"`
}

var dbConfig *DBConfig

func main() {
	// Load the database configuration injected by ECS.
	if err := loadDBConfig(); err != nil {
		log.Fatalf("failed to load DB configuration: %v", err)
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/", rootHandler)
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/secret", secretHandler)
	mux.HandleFunc("/rds", rdsHandler)
	mux.HandleFunc("/internet", internetHandler)

	server := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("server listening on :%s", port)

	if err := server.ListenAndServe(); err != nil &&
		err != http.ErrServerClosed {
		log.Fatalf("server failed: %v", err)
	}
}

func loadDBConfig() error {
	fmt.Println("*****AVAILABLE ENV VARIABLES***")
	for _, kv := range os.Environ() {
		fmt.Println(kv)
	}
	secret := os.Getenv("my_db_My_test_project_0beb8486_storage_rds_secret")

	if secret == "" {
		return fmt.Errorf("DB_CONFIG environment variable is required")
	}

	var secretConfig SecretConfig

	if err := json.Unmarshal(
		[]byte(secret),
		&secretConfig,
	); err != nil {
		return fmt.Errorf("failed to parse DB_CONFIG: %w", err)
	}

	if secretConfig.Username == "" {
		return fmt.Errorf("DB_CONFIG username is missing")
	}

	if secretConfig.Password == "" {
		return fmt.Errorf("DB_CONFIG password is missing")
	}

	config := DBConfig{
		Secrets: secretConfig,
		Host:    os.Getenv("my_db_My_test_project_0beb8486_storage_rds_db_host"),
		Port:    5432,
		DBName:  os.Getenv("my_db_My_test_project_0beb8486_storage_rds_db_name"),
	}

	if config.Host == "" {
		return fmt.Errorf("DB_CONFIG host is missing")
	}

	if config.Port == 0 {
		return fmt.Errorf("DB_CONFIG port is missing")
	}

	if config.DBName == "" {
		return fmt.Errorf("DB_CONFIG dbname is missing")
	}

	dbConfig = &config

	return nil
}

// GET /
func rootHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, CheckResponse{
		Status:  "ok",
		Message: "RDS/network diagnostic service",
		Details: map[string]string{
			"health":   "/health",
			"secret":   "/secret",
			"rds":      "/rds",
			"internet": "/internet",
		},
	})
}

// GET /health
func healthHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, CheckResponse{
		Status:  "ok",
		Message: "application is running",
	})
}

// GET /secret
func secretHandler(w http.ResponseWriter, r *http.Request) {
	if dbConfig == nil {
		writeJSON(w, http.StatusInternalServerError, CheckResponse{
			Status:  "error",
			Message: "database configuration is not loaded",
		})
		return
	}

	writeJSON(w, http.StatusOK, CheckResponse{
		Status:  "ok",
		Message: "database secret successfully injected and parsed",
		Details: map[string]any{
			"host":     dbConfig.Host,
			"port":     dbConfig.Port,
			"database": dbConfig.DBName,
			"username": dbConfig.Secrets.Username,
		},
	})
}

// GET /rds
func rdsHandler(w http.ResponseWriter, r *http.Request) {
	if dbConfig == nil {
		writeJSON(w, http.StatusInternalServerError, CheckResponse{
			Status:  "error",
			Message: "database configuration is not loaded",
		})
		return
	}

	ctx, cancel := context.WithTimeout(
		r.Context(),
		10*time.Second,
	)
	defer cancel()

	dsn := fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s",
		dbConfig.Secrets.Username,
		dbConfig.Secrets.Password,
		dbConfig.Host,
		dbConfig.Port,
		dbConfig.DBName,
	)

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, CheckResponse{
			Status:  "error",
			Message: "failed to connect to RDS",
			Details: err.Error(),
		})
		return
	}
	defer conn.Close(context.Background())

	var result int

	if err := conn.QueryRow(
		ctx,
		"SELECT 1",
	).Scan(&result); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, CheckResponse{
			Status:  "error",
			Message: "RDS connection succeeded but query failed",
			Details: err.Error(),
		})
		return
	}

	var version string

	if err := conn.QueryRow(
		ctx,
		"SELECT version()",
	).Scan(&version); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, CheckResponse{
			Status:  "error",
			Message: "RDS query succeeded but version query failed",
			Details: err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, CheckResponse{
		Status:  "ok",
		Message: "RDS connection successful",
		Details: map[string]any{
			"query_result": result,
			"version":      version,
			"host":         dbConfig.Host,
			"port":         dbConfig.Port,
			"database":     dbConfig.DBName,
		},
	})
}

// GET /internet
func internetHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(
		r.Context(),
		10*time.Second,
	)
	defer cancel()

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		"https://checkip.amazonaws.com/",
		nil,
	)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, CheckResponse{
			Status:  "error",
			Message: "failed to create HTTP request",
			Details: err.Error(),
		})
		return
	}

	response, err := http.DefaultClient.Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, CheckResponse{
			Status:  "error",
			Message: "outbound internet request failed",
			Details: err.Error(),
		})
		return
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, CheckResponse{
			Status:  "error",
			Message: "failed to read outbound response",
			Details: err.Error(),
		})
		return
	}

	if response.StatusCode < 200 ||
		response.StatusCode >= 300 {

		writeJSON(w, http.StatusBadGateway, CheckResponse{
			Status:  "error",
			Message: "outbound request returned non-success status",
			Details: map[string]any{
				"status_code": response.StatusCode,
				"body":        string(body),
			},
		})
		return
	}

	writeJSON(w, http.StatusOK, CheckResponse{
		Status:  "ok",
		Message: "outbound internet connectivity successful",
		Details: map[string]any{
			"status_code": response.StatusCode,
			"public_ip":   string(body),
		},
	})
}

func writeJSON(
	w http.ResponseWriter,
	statusCode int,
	response CheckResponse,
) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("failed to write response: %v", err)
	}
}
