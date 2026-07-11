package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// -----------------------------------------------------------------------------
// Configuration
// -----------------------------------------------------------------------------

type config struct {
	BaseURL        string
	Port           string
	MetricsPath    string
	ScrapeInterval time.Duration
	Email          string
	Password       string
	LoginURL       string
	MeURL          string
	FlareSolverURL string
}

func loadConfig() config {
	base := "https://" + getEnvOrDefault("LACALE_API_BASE_URL", "la-cale.space")
	return config{
		BaseURL:        base,
		Port:           getEnvOrDefault("PORT", "9090"),
		MetricsPath:    getEnvOrDefault("METRICS_PATH", "/metrics"),
		Email:          os.Getenv("LACALE_EMAIL"),
		Password:       os.Getenv("LACALE_PASSWORD"),
		ScrapeInterval: parseDuration(os.Getenv("SCRAPE_INTERVAL"), 5*time.Minute),
		LoginURL:       base + "/api/internal/auth/login",
		MeURL:          base + "/api/internal/me",
		FlareSolverURL: getEnvOrDefault("FLARESOLVER_URL", "http://localhost:8191"),
	}
}

func getEnvOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// -----------------------------------------------------------------------------
// FlareSolverr — get cf_clearance cookie
// -----------------------------------------------------------------------------

type flareRequest struct {
	Cmd    string            `json:"cmd"`
	URL    string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
}

type flareResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Solution struct {
		Cookies []struct {
			Name   string `json:"name"`
			Value  string `json:"value"`
			Domain string `json:"domain"`
			Path   string `json:"path"`
		} `json:"cookies"`
		UserAgent string `json:"userAgent"`
	} `json:"solution"`
}

func flareClient() *http.Client {
	return &http.Client{Timeout: 120 * time.Second}
}

func flareEndpoint(baseURL string) string {
	return baseURL + "/v1"
}

func doFlare(flareSolverURL string, req flareRequest) (*flareResponse, error) {
	url := flareEndpoint(flareSolverURL)
	payload, _ := json.Marshal(req)
	client := flareClient()
	resp, err := client.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("flare request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result flareResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse flare response: %w\n%s", err, string(body))
	}
	if result.Status != "ok" {
		return nil, fmt.Errorf("flare error: %s — %s", result.Status, result.Message)
	}
	return &result, nil
}

// waitForFlareSolverr polls until FlareSolverr responds
func waitForFlareSolverr(flareSolverURL string) error {
	url := flareEndpoint(flareSolverURL)
	fmt.Printf("[flare] Waiting for FlareSolverr at %s ...\n", url)
	client := flareClient()
	for i := 0; i < 60; i++ {
		resp, err := client.Post(url, "application/json",
			bytes.NewReader([]byte(`{"cmd":"request.get","url":"http://www.google.com/","maxTimeout":60000}`)))
		if err == nil && resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			fmt.Println("[flare] FlareSolverr is ready")
			return nil
		}
		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("FlareSolverr not ready after 120s")
}

// getCFCookies visits the login page via FlareSolverr and returns cf_clearance cookie
func getCFCookies(flareSolverURL, baseURL string) (*http.Cookie, string, error) {
	fmt.Printf("[flare] Getting cf_clearance via FlareSolverr ...\n")
	res, err := doFlare(flareSolverURL, flareRequest{
		Cmd: "request.get",
		URL: baseURL + "/login",
	})
	if err != nil {
		return nil, "", fmt.Errorf("flare cf_clearance: %w", err)
	}

	for _, c := range res.Solution.Cookies {
		if c.Name == "cf_clearance" {
			fmt.Printf("[flare] Got cf_clearance (len=%d), UA: %s\n", len(c.Value), res.Solution.UserAgent)
			cookie := &http.Cookie{
				Name:   c.Name,
				Value:  c.Value,
				Domain: c.Domain,
				Path:   c.Path,
			}
			return cookie, res.Solution.UserAgent, nil
		}
	}
	return nil, "", fmt.Errorf("no cf_clearance cookie in FlareSolverr response")
}

// -----------------------------------------------------------------------------
// LACALEClient — direct HTTP with cf_clearance cookie
// -----------------------------------------------------------------------------

type LACALEClient struct {
	mu          sync.RWMutex
	baseURL     string
	loginURL    string
	meURL       string
	userAgent   string
	cfCookie    *http.Cookie
	sessionCookie *http.Cookie
	authenticated bool
	httpClient  *http.Client
}

func newLACALEClient(baseURL, loginURL, meURL string) *LACALEClient {
	return &LACALEClient{
		baseURL:    baseURL,
		loginURL:   loginURL,
		meURL:      meURL,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return nil
			},
		},
	}
}

func (c *LACALEClient) IsAuthenticated() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.authenticated
}

// Login POSTs credentials with cf_clearance cookie
func (c *LACALEClient) Login(email, password string) error {
	fmt.Printf("[auth] Logging in: %s\n", c.loginURL)

	formLoadedAt := time.Now().UnixMilli()
	loginBody := fmt.Sprintf(`{"email":"%s","password":"%s","formLoadedAt":%d}`, email, password, formLoadedAt)

	req, err := http.NewRequest(http.MethodPost, c.loginURL, bytes.NewReader([]byte(loginBody)))
	if err != nil {
		return fmt.Errorf("build login request: %w", err)
	}

	// Attach cookies
	c.mu.RLock()
	if c.cfCookie != nil {
		req.AddCookie(c.cfCookie)
	}
	if c.sessionCookie != nil {
		req.AddCookie(c.sessionCookie)
	}
	c.mu.RUnlock()

	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.6")
	req.Header.Set("Origin", c.baseURL)
	req.Header.Set("Referer", c.baseURL+"/login")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-origin")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("login request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("[auth] Login response: %d — %.500s\n", resp.StatusCode, string(body))

	// Capture session cookie from Set-Cookie headers
	for _, sc := range resp.Header["Set-Cookie"] {
		idx := strings.IndexByte(sc, '=')
		if idx > 0 {
			name := sc[:idx]
			if strings.HasPrefix(name, "session") || strings.HasPrefix(name, "__Host-") {
				valueEnd := strings.Index(sc[idx+1:], ";")
				var value string
				if valueEnd > 0 {
					value = sc[idx+1 : idx+1+valueEnd]
				} else {
					value = sc[idx+1:]
				}
				c.mu.Lock()
				c.sessionCookie = &http.Cookie{Name: name, Value: value}
				c.mu.Unlock()
			}
		}
	}

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("invalid credentials")
	}
	if resp.StatusCode == http.StatusBadRequest {
		return fmt.Errorf("login failed: %s", string(body))
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status: %d — %s", resp.StatusCode, string(body))
	}

	// Check for success in body
	if strings.Contains(string(body), `"success":true`) || strings.Contains(string(body), `"success": true`) {
		c.mu.Lock()
		c.authenticated = true
		c.mu.Unlock()
		fmt.Println("[auth] Login successful")
		return nil
	}

	// If we got 200 but no JSON success, still mark authenticated (might be redirect-based)
	c.mu.Lock()
	c.authenticated = true
	c.mu.Unlock()
	fmt.Println("[auth] Login successful (200 OK)")
	return nil
}

// FetchMetrics GETs /api/internal/me with cf_clearance + session cookies
func (c *LACALEClient) FetchMetrics() (*UserMetrics, error) {
	c.mu.RLock()
	auth := c.authenticated
	c.mu.RUnlock()
	if !auth {
		return nil, fmt.Errorf("not authenticated")
	}

	req, err := http.NewRequest(http.MethodGet, c.meURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build metrics request: %w", err)
	}

	c.mu.RLock()
	if c.cfCookie != nil {
		req.AddCookie(c.cfCookie)
	}
	if c.sessionCookie != nil {
		req.AddCookie(c.sessionCookie)
	}
	c.mu.RUnlock()

	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.6")
	req.Header.Set("Origin", c.baseURL)
	req.Header.Set("Referer", c.baseURL)
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-origin")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("metrics request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("[metrics] Response: %d — %.500s\n", resp.StatusCode, string(body))

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("unauthorized")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	return parseMetrics(body)
}

// -----------------------------------------------------------------------------
// Domain types
// -----------------------------------------------------------------------------

type UserMetrics struct {
	TotalUploadedBytes   int64
	TotalDownloadedBytes int64
	Username             string
}

type dataWrapperWithUser struct {
	Data struct {
		User struct {
			Uploaded   int64  `json:"uploaded"`
			Downloaded int64  `json:"downloaded"`
			Username   string `json:"username"`
		} `json:"user"`
	} `json:"data"`
}

type dataWrapper struct {
	Data struct {
		Uploaded   int64  `json:"uploaded"`
		Downloaded int64  `json:"downloaded"`
		Username   string `json:"username"`
	} `json:"data"`
}

type directResponse struct {
	Uploaded   int64  `json:"uploaded"`
	Downloaded int64  `json:"downloaded"`
	Username   string `json:"username"`
}

type userResponseWrapper struct {
	User struct {
		Uploaded   int64  `json:"uploaded"`
		Downloaded int64  `json:"downloaded"`
		Username   string `json:"username"`
	} `json:"user"`
}

func parseMetrics(body []byte) (*UserMetrics, error) {
	var w1 dataWrapperWithUser
	if err := json.Unmarshal(body, &w1); err == nil && w1.Data.User.Uploaded > 0 {
		return &UserMetrics{TotalUploadedBytes: w1.Data.User.Uploaded, TotalDownloadedBytes: w1.Data.User.Downloaded, Username: w1.Data.User.Username}, nil
	}
	var w2 dataWrapper
	if err := json.Unmarshal(body, &w2); err == nil && w2.Data.Uploaded > 0 {
		return &UserMetrics{TotalUploadedBytes: w2.Data.Uploaded, TotalDownloadedBytes: w2.Data.Downloaded, Username: w2.Data.Username}, nil
	}
	var d directResponse
	if err := json.Unmarshal(body, &d); err == nil && d.Uploaded > 0 {
		return &UserMetrics{TotalUploadedBytes: d.Uploaded, TotalDownloadedBytes: d.Downloaded, Username: d.Username}, nil
	}
	var u userResponseWrapper
	if err := json.Unmarshal(body, &u); err == nil && u.User.Uploaded > 0 {
		return &UserMetrics{TotalUploadedBytes: u.User.Uploaded, TotalDownloadedBytes: u.User.Downloaded, Username: u.User.Username}, nil
	}
	return nil, fmt.Errorf("could not parse metrics: %.500s", string(body))
}

// -----------------------------------------------------------------------------
// Prometheus
// -----------------------------------------------------------------------------

type exporterMetrics struct {
	totalUploaded   prometheus.Gauge
	totalDownloaded prometheus.Gauge
}

func newExporterMetrics(reg prometheus.Registerer) *exporterMetrics {
	m := &exporterMetrics{
		totalUploaded: prometheus.NewGauge(prometheus.GaugeOpts{Namespace: "lacale", Name: "total_uploaded_bytes", Help: "Total uploaded bytes from La-Cale API"}),
		totalDownloaded: prometheus.NewGauge(prometheus.GaugeOpts{Namespace: "lacale", Name: "total_downloaded_bytes", Help: "Total downloaded bytes from La-Cale API"}),
	}
	reg.MustRegister(m.totalUploaded, m.totalDownloaded)
	return m
}

func (m *exporterMetrics) update(u *UserMetrics) {
	m.totalUploaded.Set(float64(u.TotalUploadedBytes))
	m.totalDownloaded.Set(float64(u.TotalDownloadedBytes))
}

// -----------------------------------------------------------------------------
// Server
// -----------------------------------------------------------------------------

type server struct {
	mu             sync.RWMutex
	client         *LACALEClient
	metrics        *exporterMetrics
	lastMetrics    *UserMetrics
	email          string
	password       string
	flareSolverURL string
	baseURL        string
}

func (s *server) metricsHandler(c *gin.Context) {
	c.Header("Content-Type", "text/plain; version=0.0.4")
	promhttp.Handler().ServeHTTP(c.Writer, c.Request)
}

// reconnect re-fetches cf_clearance via FlareSolverr and re-login
func (s *server) reconnect() error {
	fmt.Println("[reconnect] Attempting reconnection...")
	cfCookie, ua, err := getCFCookies(s.flareSolverURL, s.baseURL)
	if err != nil {
		return fmt.Errorf("reconnect cf_clearance: %w", err)
	}
	s.client.mu.Lock()
	s.client.cfCookie = cfCookie
	s.client.userAgent = ua
	s.client.mu.Unlock()

	if err := s.client.Login(s.email, s.password); err != nil {
		return fmt.Errorf("reconnect login: %w", err)
	}
	fmt.Println("[reconnect] Reconnected successfully")
	return nil
}

func (s *server) scrapeAndRefresh() {
	// First attempt
	m, err := s.client.FetchMetrics()
	if err != nil {
		fmt.Printf("[scraper] Fetch failed: %v — attempting reconnect...\n", err)

		// Reconnect and retry once
		if reconnectErr := s.reconnect(); reconnectErr != nil {
			fmt.Printf("[scraper] Reconnect failed: %v\n", reconnectErr)
			return
		}

		m, err = s.client.FetchMetrics()
		if err != nil {
			fmt.Printf("[scraper] Fetch still failing after reconnect: %v\n", err)
			return
		}
	}

	s.metrics.update(m)
	s.mu.Lock()
	s.lastMetrics = m
	s.mu.Unlock()
	fmt.Printf("[scraper] %s (↑%d ↓%d)\n", m.Username, m.TotalUploadedBytes, m.TotalDownloadedBytes)
}

func (s *server) startMetricsRefresher(interval time.Duration) {
	go s.scrapeAndRefresh()
	go func() {
		for range time.NewTicker(interval).C {
			s.scrapeAndRefresh()
		}
	}()
}

func (s *server) healthHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "healthy", "authenticated": s.client.IsAuthenticated()})
}

// -----------------------------------------------------------------------------
// Main
// -----------------------------------------------------------------------------

func main() {
	cfg := loadConfig()
	client := newLACALEClient(cfg.BaseURL, cfg.LoginURL, cfg.MeURL)
	metrics := newExporterMetrics(prometheus.DefaultRegisterer)
	srv := &server{
		client:         client,
		metrics:        metrics,
		email:          cfg.Email,
		password:       cfg.Password,
		flareSolverURL: cfg.FlareSolverURL,
		baseURL:        cfg.BaseURL,
	}

	if cfg.Email != "" && cfg.Password != "" {
		// Wait for FlareSolverr
		if err := waitForFlareSolverr(cfg.FlareSolverURL); err != nil {
			fmt.Printf("[flare] FlareSolverr not ready: %v (proceeding anyway)\n", err)
		}

		// Get cf_clearance cookie + User-Agent
		cfCookie, ua, err := getCFCookies(cfg.FlareSolverURL, cfg.BaseURL)
		if err != nil {
			fmt.Printf("[flare] Failed to get cf_clearance: %v\n", err)
		} else {
			client.mu.Lock()
			client.cfCookie = cfCookie
			client.userAgent = ua
			client.mu.Unlock()

			// Login immediately with fresh cookie
			fmt.Println("[auth] Attempting auto-login...")
			if err := client.Login(cfg.Email, cfg.Password); err != nil {
				fmt.Printf("[auth] Auto-login failed: %v\n", err)
			}
		}
	} else {
		fmt.Println("[auth] No credentials, skipping login")
	}

	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()
	r.GET(cfg.MetricsPath, srv.metricsHandler)
	r.GET("/health", srv.healthHandler)

	srv.startMetricsRefresher(cfg.ScrapeInterval)
	fmt.Printf("[server] Refresh interval: %v\n", cfg.ScrapeInterval)

	addr := ":" + cfg.Port
	fmt.Printf("[server] Listening on %s\n", addr)
	if err := r.Run(addr); err != nil {
		fmt.Printf("[server] Error: %v\n", err)
		os.Exit(1)
	}
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

func parseDuration(s string, fallback time.Duration) time.Duration {
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback
	}
	for _, u := range []struct{ suffix string; mult time.Duration }{
		{"h", time.Hour}, {"m", time.Minute}, {"s", time.Second},
	} {
		if strings.HasSuffix(s, u.suffix) {
			var n int
			if _, err := fmt.Sscanf(strings.TrimSuffix(s, u.suffix), "%d", &n); err == nil && n > 0 {
				return time.Duration(n) * u.mult
			}
		}
	}
	return fallback
}
