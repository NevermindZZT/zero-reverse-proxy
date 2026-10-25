package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
	"zero-reverse-proxy/internal/handler"
	"zero-reverse-proxy/internal/proxy"
	"zero-reverse-proxy/internal/storage"
	"zero-reverse-proxy/web"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("Starting Zero Reverse Proxy...")

	// 1. Resolve configuration path
	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "data/config.json"
	}

	// 2. Initialize storage
	store, err := storage.NewStorage(configPath)
	if err != nil {
		log.Fatalf("Failed to initialize storage: %v", err)
	}

	// 3. Override config from environment variables if provided
	cfg := store.GetConfig()
	if envPort := os.Getenv("PORT"); envPort != "" {
		if p, err := strconv.Atoi(envPort); err == nil && p > 0 {
			cfg.Port = p
		}
	}
	adminUser := os.Getenv("ADMIN_USER")
	if adminUser == "" {
		adminUser = "admin"
	}
	adminPass := os.Getenv("ADMIN_PASS")
	if adminPass == "" {
		adminPass = "admin123"
	}
	_ = store.SetAdminCredentials(adminUser, adminPass)

	// 4. Initialize Proxy Engine and sync rules
	engine := proxy.NewEngine()
	engine.UpdateRules(store.ListRules())

	// Subscribe to rule changes for zero-downtime hot reloading
	store.OnChange(func(rules []storage.Rule) {
		log.Printf("[Storage] Rules updated, reloading %d rules into proxy engine", len(rules))
		engine.UpdateRules(rules)
	})

	// 5. Setup internal router for admin UI and API
	mux := http.NewServeMux()

	// API endpoints
	apiHandler := handler.NewAPIHandler(store)
	apiHandler.RegisterRoutes(mux)

	// Web UI (Embedded static files)
	webHandler := http.StripPrefix("/admin", web.Handler())
	mux.Handle("/admin/", webHandler)
	mux.HandleFunc("GET /admin", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/", http.StatusMovedPermanently)
	})

	// Root redirect to /admin/
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/", http.StatusFound)
	})

	// 6. Master dispatcher handler
	masterHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// First check if the request matches a reverse proxy rule (direct or referer fallback)
		if engine.ServeHTTP(w, r) {
			return
		}

		// Otherwise forward to admin UI & API mux
		mux.ServeHTTP(w, r)
	})

	// 7. Start HTTP server
	addr := fmt.Sprintf("0.0.0.0:%d", cfg.Port)
	server := &http.Server{
		Addr:         addr,
		Handler:      masterHandler,
		ReadTimeout:  120 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Printf("Zero Reverse Proxy is listening on http://0.0.0.0:%d", cfg.Port)
		log.Printf("Admin Dashboard: http://0.0.0.0:%d/admin/", cfg.Port)
		log.Printf("Proxy Entry:     http://0.0.0.0:%d/p/{slug}/", cfg.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server ListenAndServe failed: %v", err)
		}
	}()

	// 8. Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down server gracefully...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}
	log.Println("Server exited properly.")
}
