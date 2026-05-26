package viz

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"llm-topology/internal/topology"
	"llm-topology/internal/topology/domain"
)

func loadEnvFile(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		if os.Getenv(key) == "" {
			os.Setenv(key, val)
		}
	}
}

func envPort() int {
	loadEnvFile(".env")
	raw := os.Getenv("VIZ_PORT")
	if raw == "" {
		return 57800
	}
	p, err := strconv.Atoi(raw)
	if err != nil || p < 1 || p > 65535 {
		return 57800
	}
	return p
}

func Serve(manager *topology.TopologyManager, frontendFS fs.FS) error {
	dist, err := fs.Sub(frontendFS, "frontend/dist")
	if err != nil {
		return fmt.Errorf("embedded frontend not found (run 'cd frontend && npm run build' first): %w", err)
	}

	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/topology", func(w http.ResponseWriter, r *http.Request) {
		topo, err := manager.ReadAll()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(topo)
	})

	mux.HandleFunc("GET /api/function/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		ctx, err := manager.ReadFunction(id,
			topology.WithResourceFilter(
				domain.STRUCT_RESOURCE,
				domain.INTERFACE_RESOURCE,
				domain.EXTERNAL_VAR_RESOURCE,
			),
		)
		if err != nil {
			http.Error(w, err.Error(), 404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ctx)
	})

	mux.HandleFunc("GET /api/struct/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		ctx, err := manager.ReadStruct(id,
			topology.WithResourceFilter(
				domain.STRUCT_RESOURCE,
				domain.INTERFACE_RESOURCE,
				domain.FUNCTION_RESOURCE,
			),
		)
		if err != nil {
			http.Error(w, err.Error(), 404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ctx)
	})

	fileServer := http.FileServer(http.FS(dist))
	mux.Handle("/", fileServer)

	port := envPort()
	listener, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", port))
	if err != nil {
		return fmt.Errorf("failed to listen on port %d: %w", port, err)
	}

	addr := fmt.Sprintf("http://localhost:%d", port)

	fmt.Printf("┌──────────────────────────────────────────────┐\n")
	fmt.Printf("│  llm-topology visualizer                     │\n")
	fmt.Printf("│                                              │\n")
	fmt.Printf("│  Open in browser: %s               │\n", addr)
	fmt.Printf("│  Press Ctrl+C to stop                        │\n")
	fmt.Printf("└──────────────────────────────────────────────┘\n")

	_ = os.MkdirAll(filepath.Dir(".ltp/topology.db"), 0755)

	go func() {
		time.Sleep(500 * time.Millisecond)
		openBrowser(addr)
	}()

	return http.Serve(listener, mux)
}

func ServeWithFile(distroPath string, manager *topology.TopologyManager) error {
	sub, err := fs.Sub(os.DirFS(distroPath), ".")
	if err != nil {
		return err
	}
	return Serve(manager, sub)
}

func openBrowser(url string) {}
