package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"spotiflac/backend"
)

var (
	spotifyTrackRegex = regexp.MustCompile(`(?i)(?:spotify:track:|https?://open\.spotify\.com/(?:intl-[^/]+/)?track/)([A-Za-z0-9]{22})`)
	spotifyIDRegex    = regexp.MustCompile(`^[A-Za-z0-9]{22}$`)
	validServices     = map[string]struct{}{
		"tidal":  {},
		"qobuz":  {},
		"amazon": {},
	}
	defaultServices = []string{"tidal", "qobuz", "amazon"}
)

type trackMetadata struct {
	SpotifyID   string `json:"spotify_id"`
	Artists     string `json:"artists"`
	Name        string `json:"name"`
	AlbumName   string `json:"album_name"`
	AlbumArtist string `json:"album_artist"`
	Images      string `json:"images"`
	ReleaseDate string `json:"release_date"`
	TrackNumber int    `json:"track_number"`
	TotalTracks int    `json:"total_tracks"`
	DiscNumber  int    `json:"disc_number"`
	TotalDiscs  int    `json:"total_discs"`
	Copyright   string `json:"copyright"`
	Publisher   string `json:"publisher"`
}

type trackResponse struct {
	Track trackMetadata `json:"track"`
}

type attempt struct {
	Service string `json:"service"`
	Error   string `json:"error,omitempty"`
}

type createDownloadRequest struct {
	SpotifyURL string   `json:"spotify_url"`
	Services   []string `json:"services,omitempty"`
	TTLSeconds int      `json:"ttl_seconds,omitempty"`
}

type createDownloadResponse struct {
	OK          bool      `json:"ok"`
	SpotifyID   string    `json:"spotify_id,omitempty"`
	Service     string    `json:"service,omitempty"`
	Filename    string    `json:"filename,omitempty"`
	DownloadURL string    `json:"download_url,omitempty"`
	ExpiresAt   time.Time `json:"expires_at,omitempty"`
	Attempts    []attempt `json:"attempts,omitempty"`
	Error       string    `json:"error,omitempty"`
}

type errorResponse struct {
	OK       bool      `json:"ok"`
	Error    string    `json:"error"`
	Attempts []attempt `json:"attempts,omitempty"`
}

type downloadEntry struct {
	Token     string
	Path      string
	Service   string
	SpotifyID string
	ExpiresAt time.Time
	CreatedAt time.Time
}

type downloadStore struct {
	mu      sync.RWMutex
	entries map[string]downloadEntry
}

func newDownloadStore() *downloadStore {
	return &downloadStore{
		entries: make(map[string]downloadEntry),
	}
}

func (s *downloadStore) put(path, service, spotifyID string, ttl time.Duration) (downloadEntry, error) {
	token, err := generateToken()
	if err != nil {
		return downloadEntry{}, err
	}

	entry := downloadEntry{
		Token:     token,
		Path:      path,
		Service:   service,
		SpotifyID: spotifyID,
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(ttl),
	}

	s.mu.Lock()
	s.entries[token] = entry
	s.mu.Unlock()

	return entry, nil
}

func (s *downloadStore) get(token string) (downloadEntry, bool) {
	s.mu.RLock()
	entry, ok := s.entries[token]
	s.mu.RUnlock()
	return entry, ok
}

func (s *downloadStore) delete(token string) {
	s.mu.Lock()
	entry, ok := s.entries[token]
	if ok {
		delete(s.entries, token)
	}
	s.mu.Unlock()

	if ok {
		_ = os.Remove(entry.Path)
		_ = os.Remove(filepath.Dir(entry.Path))
		_ = os.Remove(filepath.Dir(filepath.Dir(entry.Path)))
	}
}

func (s *downloadStore) cleanupExpired() {
	now := time.Now()
	var expired []string

	s.mu.RLock()
	for token, entry := range s.entries {
		if now.After(entry.ExpiresAt) {
			expired = append(expired, token)
		}
	}
	s.mu.RUnlock()

	for _, token := range expired {
		s.delete(token)
	}
}

func (s *downloadStore) startCleanupLoop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.cleanupExpired()
		case <-ctx.Done():
			return
		}
	}
}

type apiServer struct {
	store   *downloadStore
	baseURL string
	ttl     time.Duration
}

func main() {
	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		port = "8080"
	}

	ttl := 2 * time.Hour
	if rawTTL := strings.TrimSpace(os.Getenv("DOWNLOAD_TTL")); rawTTL != "" {
		parsed, err := time.ParseDuration(rawTTL)
		if err != nil {
			log.Fatalf("invalid DOWNLOAD_TTL %q: %v", rawTTL, err)
		}
		if parsed <= 0 {
			log.Fatalf("invalid DOWNLOAD_TTL %q: must be > 0", rawTTL)
		}
		ttl = parsed
	}

	server := &apiServer{
		store:   newDownloadStore(),
		baseURL: strings.TrimSpace(os.Getenv("BASE_URL")),
		ttl:     ttl,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go server.store.startCleanupLoop(ctx, 1*time.Minute)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", server.handleHealth)
	mux.HandleFunc("/v1/download-url", server.handleCreateDownloadURL)
	mux.HandleFunc("/v1/download/", server.handleDownloadByToken)
	mux.HandleFunc("/", server.handleRoot)

	httpServer := &http.Server{
		Addr:              ":" + port,
		Handler:           withCORS(mux),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
	}

	log.Printf("REST API listening on http://localhost:%s", port)
	log.Printf("Token TTL: %s", ttl)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("server error: %v", err)
	}
}

func (s *apiServer) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		writeJSON(w, http.StatusNotFound, errorResponse{
			OK:    false,
			Error: "route not found",
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"name":      "SpotiFLAC REST API",
		"version":   "1",
		"endpoints": []string{"GET /health", "POST /v1/download-url", "GET /v1/download/{token}"},
	})
}

func (s *apiServer) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":  true,
		"now": time.Now().UTC(),
	})
}

func (s *apiServer) handleCreateDownloadURL(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, errorResponse{OK: false, Error: "method not allowed"})
		return
	}

	var req createDownloadRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{OK: false, Error: "invalid JSON body"})
		return
	}

	if strings.TrimSpace(req.SpotifyURL) == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{OK: false, Error: "spotify_url is required"})
		return
	}

	serviceOrder := normalizeServiceOrder(req.Services)
	if len(serviceOrder) == 0 {
		writeJSON(w, http.StatusBadRequest, errorResponse{OK: false, Error: "no valid services in services[]"})
		return
	}

	ttl := s.ttl
	if req.TTLSeconds > 0 {
		override := time.Duration(req.TTLSeconds) * time.Second
		if override > 24*time.Hour {
			override = 24 * time.Hour
		}
		ttl = override
	}

	downloadPath, serviceUsed, spotifyID, attempts, err := resolveWithFallback(req.SpotifyURL, serviceOrder)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, createDownloadResponse{
			OK:       false,
			Error:    err.Error(),
			Attempts: attempts,
		})
		return
	}

	entry, err := s.store.put(downloadPath, serviceUsed, spotifyID, ttl)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{OK: false, Error: "failed to generate download token"})
		return
	}

	downloadURL := fmt.Sprintf("%s/v1/download/%s", s.publicBaseURL(r), entry.Token)
	writeJSON(w, http.StatusOK, createDownloadResponse{
		OK:          true,
		SpotifyID:   spotifyID,
		Service:     serviceUsed,
		Filename:    filepath.Base(entry.Path),
		DownloadURL: downloadURL,
		ExpiresAt:   entry.ExpiresAt.UTC(),
		Attempts:    attempts,
	})
}

func (s *apiServer) handleDownloadByToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, errorResponse{OK: false, Error: "method not allowed"})
		return
	}

	prefix := "/v1/download/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		writeJSON(w, http.StatusNotFound, errorResponse{OK: false, Error: "route not found"})
		return
	}

	token := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, prefix))
	if token == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{OK: false, Error: "missing token"})
		return
	}

	entry, ok := s.store.get(token)
	if !ok {
		writeJSON(w, http.StatusNotFound, errorResponse{OK: false, Error: "invalid or expired token"})
		return
	}

	if time.Now().After(entry.ExpiresAt) {
		s.store.delete(token)
		writeJSON(w, http.StatusGone, errorResponse{OK: false, Error: "download token expired"})
		return
	}

	file, err := os.Open(entry.Path)
	if err != nil {
		s.store.delete(token)
		writeJSON(w, http.StatusNotFound, errorResponse{OK: false, Error: "file no longer available"})
		return
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{OK: false, Error: "unable to read file metadata"})
		return
	}

	filename := filepath.Base(entry.Path)
	contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(filename)))
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size()))
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, filename, info.ModTime(), file)
}

func resolveWithFallback(spotifyInput string, serviceOrder []string) (downloadPath, serviceUsed, spotifyID string, attempts []attempt, err error) {
	spotifyID, err = extractSpotifyTrackID(spotifyInput)
	if err != nil {
		return "", "", "", nil, err
	}

	spotifyURL := "https://open.spotify.com/track/" + spotifyID
	meta, err := fetchTrackMetadata(spotifyURL)
	if err != nil {
		return "", "", spotifyID, nil, fmt.Errorf("failed to fetch Spotify metadata: %w", err)
	}

	workDir, err := os.MkdirTemp("", "spotiflac-rest-")
	if err != nil {
		return "", "", spotifyID, nil, fmt.Errorf("failed to create temp directory: %w", err)
	}

	for _, service := range serviceOrder {
		serviceDir := filepath.Join(workDir, service)
		filename, dlErr := runServiceDownload(service, spotifyID, spotifyURL, meta, serviceDir)
		if dlErr != nil {
			attempts = append(attempts, attempt{Service: service, Error: dlErr.Error()})
			continue
		}

		filename = strings.TrimPrefix(filename, "EXISTS:")
		if filename == "" {
			attempts = append(attempts, attempt{Service: service, Error: "empty file path returned"})
			continue
		}

		stat, statErr := os.Stat(filename)
		if statErr != nil {
			attempts = append(attempts, attempt{Service: service, Error: fmt.Sprintf("downloaded file missing: %v", statErr)})
			continue
		}
		if stat.Size() <= 0 {
			attempts = append(attempts, attempt{Service: service, Error: "downloaded file is empty"})
			continue
		}

		attempts = append(attempts, attempt{Service: service})
		return filename, service, spotifyID, attempts, nil
	}

	_ = os.RemoveAll(workDir)
	return "", "", spotifyID, attempts, fmt.Errorf("failed in all services: %s", strings.Join(serviceOrder, " -> "))
}

func runServiceDownload(service, spotifyID, spotifyURL string, meta trackMetadata, outputDir string) (string, error) {
	switch service {
	case "tidal":
		downloader := backend.NewTidalDownloader("")
		return downloader.Download(
			spotifyID,
			outputDir,
			"LOSSLESS",
			"title-artist",
			false,
			0,
			meta.Name,
			meta.Artists,
			meta.AlbumName,
			meta.AlbumArtist,
			meta.ReleaseDate,
			false,
			meta.Images,
			true,
			meta.TrackNumber,
			meta.DiscNumber,
			meta.TotalTracks,
			meta.TotalDiscs,
			meta.Copyright,
			meta.Publisher,
			spotifyURL,
			true,
			false,
		)

	case "qobuz":
		downloader := backend.NewQobuzDownloader()
		return downloader.DownloadTrack(
			spotifyID,
			outputDir,
			"6",
			"title-artist",
			false,
			0,
			meta.Name,
			meta.Artists,
			meta.AlbumName,
			meta.AlbumArtist,
			meta.ReleaseDate,
			false,
			meta.Images,
			true,
			meta.TrackNumber,
			meta.DiscNumber,
			meta.TotalTracks,
			meta.TotalDiscs,
			meta.Copyright,
			meta.Publisher,
			spotifyURL,
			true,
			false,
		)

	case "amazon":
		downloader := backend.NewAmazonDownloader()
		return downloader.DownloadBySpotifyID(
			spotifyID,
			outputDir,
			"flac",
			"title-artist",
			"",
			"",
			false,
			0,
			meta.Name,
			meta.Artists,
			meta.AlbumName,
			meta.AlbumArtist,
			meta.ReleaseDate,
			meta.Images,
			meta.TrackNumber,
			meta.DiscNumber,
			meta.TotalTracks,
			true,
			meta.TotalDiscs,
			meta.Copyright,
			meta.Publisher,
			spotifyURL,
			false,
		)
	default:
		return "", fmt.Errorf("unsupported service: %s", service)
	}
}

func fetchTrackMetadata(spotifyURL string) (trackMetadata, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	data, err := backend.GetFilteredSpotifyData(ctx, spotifyURL, false, 0)
	if err != nil {
		return trackMetadata{}, err
	}

	raw, err := json.Marshal(data)
	if err != nil {
		return trackMetadata{}, err
	}

	var payload trackResponse
	if err := json.Unmarshal(raw, &payload); err != nil {
		return trackMetadata{}, err
	}

	if strings.TrimSpace(payload.Track.Name) == "" {
		return trackMetadata{}, fmt.Errorf("spotify metadata did not include track name")
	}

	return payload.Track, nil
}

func extractSpotifyTrackID(input string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", fmt.Errorf("spotify URL is empty")
	}

	if spotifyIDRegex.MatchString(input) {
		return input, nil
	}

	matches := spotifyTrackRegex.FindStringSubmatch(input)
	if len(matches) < 2 {
		return "", fmt.Errorf("invalid Spotify track URL or ID")
	}

	return matches[1], nil
}

func normalizeServiceOrder(services []string) []string {
	if len(services) == 0 {
		return append([]string(nil), defaultServices...)
	}

	seen := make(map[string]struct{})
	normalized := make([]string, 0, len(services))

	for _, service := range services {
		service = strings.ToLower(strings.TrimSpace(service))
		if _, ok := validServices[service]; !ok {
			continue
		}
		if _, exists := seen[service]; exists {
			continue
		}
		seen[service] = struct{}{}
		normalized = append(normalized, service)
	}

	return normalized
}

func (s *apiServer) publicBaseURL(r *http.Request) string {
	if s.baseURL != "" {
		return strings.TrimRight(s.baseURL, "/")
	}

	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwardedProto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); forwardedProto != "" {
		scheme = forwardedProto
	}

	return fmt.Sprintf("%s://%s", scheme, r.Host)
}

func generateToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
