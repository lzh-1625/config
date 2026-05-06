// This file demonstrates all features of the config handler package.
// Run it with:
//
//	go run ./example
//
// Then open http://localhost:8080/config in your browser.
// The secret key for the login page is "demo-secret".
package main

import (
	"fmt"
	"log"
	"net/http"
	"time"

	config "github.com/lzh-1625/config"
)

// TLSConfig is a nested struct; it will be rendered as an editable JSON block.
type TLSConfig struct {
	CertFile string `json:"cert_file" config:"Path to the TLS certificate file"`
	KeyFile  string `json:"key_file"  config:"Path to the TLS private key file"`
}

// AppConfig is the application's runtime configuration.
// Each exported field becomes a form control in the web UI.
// Use the `config` struct tag to provide a human-readable description.
// Set `config:"-"` to hide a field from the UI entirely.
type AppConfig struct {
	Host        string            `config:"Bind address for the HTTP server"`
	Port        int               `config:"Port the server listens on"`
	Debug       bool              `config:"Enable verbose debug logging"`
	LogLevel    string            `config:"Log level: debug | info | warn | error"`
	Timeout     time.Duration     `config:"Request read/write timeout"`
	StartAt     time.Time         `config:"Scheduled start time (leave empty for immediate)"`
	AllowHosts  []string          `config:"Allowed host names (comma-separated in the UI)"`
	AllowPorts  []int             `config:"Additional ports to accept traffic on"`
	MaxBodySize int64             `config:"Maximum request body size in bytes"`
	RateLimit   float64           `config:"Maximum requests per second (0 = unlimited)"`
	Labels      map[string]string `config:"Arbitrary key-value labels attached to every request"`
	TLS         TLSConfig         `config:"TLS certificate configuration"`
	// OnPing is func(): the UI shows an Invoke button. Omit from JSON/YAML so
	// WithFile persistence still works (functions are not serializable).
	OnPing      func()         `json:"-" yaml:"-" config:"Log a ping to the server console"`
	internalKey string         `config:"-"` // unexported; also hidden by config:"-"
}

func main() {
	cfg := &AppConfig{
		Host:        "0.0.0.0",
		Port:        8080,
		Debug:       false,
		LogLevel:    "info",
		Timeout:     30 * time.Second,
		AllowHosts:  []string{"localhost", "example.com"},
		AllowPorts:  []int{80, 443},
		MaxBodySize: 1 << 20, // 1 MiB
		RateLimit:   100,
		Labels:      map[string]string{"env": "development", "region": "us-east-1"},
		TLS:         TLSConfig{CertFile: "cert.pem", KeyFile: "key.pem"},
		OnPing:      func() { log.Println("[config] OnPing invoked") },
	}

	// Mount the config UI at /config.
	//
	//   WithSecret  – password-protect the UI; visitors must enter the key
	//                 before making any changes.
	//   WithFile    – persist config to disk; format is inferred from the
	//                 file extension (.yaml/.yml → YAML, anything else → JSON).
	//                 The file is loaded on startup and written after every save.
	//   WithHook    – callback fired after each successful save; receives a
	//                 deep copy of the config before and after the change.
	http.Handle("/config", config.NewConfigHandler(cfg,
		config.WithSecret[AppConfig]("demo-secret"),
		config.WithFile[AppConfig]("config.yaml"),
		config.WithHook(func(old, new AppConfig) {
			log.Printf("[config] changed\n  before: %+v\n  after:  %+v", old, new)
		}),
	))

	// Any other handler can read *cfg directly; changes from the UI are
	// reflected immediately without a restart.
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Current config:\n%+v\n", *cfg)
	})

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	log.Printf("Server listening on http://%s", addr)
	log.Printf("Config UI:  http://localhost:%d/config  (secret: demo-secret)", cfg.Port)
	log.Fatal(http.ListenAndServe(addr, nil))
}
