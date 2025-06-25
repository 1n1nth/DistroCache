package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// CacheItem represents a cached item with metadata
type CacheItem struct {
	Key         string                 `json:"key"`
	Value       interface{}            `json:"value"`
	TTL         time.Duration          `json:"ttl"`
	CreatedAt   time.Time              `json:"created_at"`
	AccessedAt  time.Time              `json:"accessed_at"`
	AccessCount int64                  `json:"access_count"`
	Tags        []string               `json:"tags,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// IsExpired checks if the cache item has expired
func (ci *CacheItem) IsExpired() bool {
	if ci.TTL == 0 {
		return false // Never expires
	}
	return time.Since(ci.CreatedAt) > ci.TTL
}

// DistroCache represents the main cache structure
type DistroCache struct {
	data      map[string]*CacheItem
	tagIndex  map[string][]string // tag -> keys
	mutex     sync.RWMutex
	stats     *CacheStats
	config    *CacheConfig
	replicaMu sync.RWMutex
	replicas  []string
}

// CacheConfig holds configuration for the cache
type CacheConfig struct {
	MaxSize           int           `json:"max_size"`
	DefaultTTL        time.Duration `json:"default_ttl"`
	CleanupInterval   time.Duration `json:"cleanup_interval"`
	Port              int           `json:"port"`
	NodeID            string        `json:"node_id"`
	ReplicationFactor int           `json:"replication_factor"`
}

// CacheStats tracks cache performance metrics
type CacheStats struct {
	Hits          prometheus.Counter
	Misses        prometheus.Counter
	Sets          prometheus.Counter
	Deletes       prometheus.Counter
	Evictions     prometheus.Counter
	TotalItems    prometheus.Gauge
	MemoryUsage   prometheus.Gauge
	AvgAccessTime prometheus.Histogram
}

// NewDistroCache creates a new distributed cache instance
func NewDistroCache(config *CacheConfig) *DistroCache {
	stats := &CacheStats{
		Hits: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "distrocache_hits_total",
			Help: "Total number of cache hits",
		}),
		Misses: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "distrocache_misses_total",
			Help: "Total number of cache misses",
		}),
		Sets: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "distrocache_sets_total",
			Help: "Total number of cache sets",
		}),
		Deletes: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "distrocache_deletes_total",
			Help: "Total number of cache deletes",
		}),
		Evictions: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "distrocache_evictions_total",
			Help: "Total number of cache evictions",
		}),
		TotalItems: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "distrocache_items_total",
			Help: "Total number of items in cache",
		}),
		MemoryUsage: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "distrocache_memory_bytes",
			Help: "Memory usage in bytes",
		}),
		AvgAccessTime: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "distrocache_access_duration_seconds",
			Help: "Cache access duration in seconds",
		}),
	}

	// Register metrics
	prometheus.MustRegister(stats.Hits, stats.Misses, stats.Sets, stats.Deletes,
		stats.Evictions, stats.TotalItems, stats.MemoryUsage, stats.AvgAccessTime)

	cache := &DistroCache{
		data:     make(map[string]*CacheItem),
		tagIndex: make(map[string][]string),
		stats:    stats,
		config:   config,
		replicas: make([]string, 0),
	}

	// Start cleanup goroutine
	go cache.startCleanup()

	return cache
}

// hashKey creates a consistent hash for key distribution
func (dc *DistroCache) hashKey(key string) string {
	hasher := sha256.Sum256([]byte(key))
	return hex.EncodeToString(hasher[:])
}

// shouldOwnKey determines if this node should own the given key
func (dc *DistroCache) shouldOwnKey(key string) bool {
	// Simple consistent hashing - in production, use proper consistent hashing
	hash := dc.hashKey(key)
	return strings.HasPrefix(hash, "0") || strings.HasPrefix(hash, "1") ||
		strings.HasPrefix(hash, "2") || strings.HasPrefix(hash, "3")
}

// Get retrieves an item from the cache
func (dc *DistroCache) Get(key string) (*CacheItem, bool) {
	start := time.Now()
	defer func() {
		dc.stats.AvgAccessTime.Observe(time.Since(start).Seconds())
	}()

	dc.mutex.RLock()
	defer dc.mutex.RUnlock()

	item, exists := dc.data[key]
	if !exists {
		dc.stats.Misses.Inc()
		return nil, false
	}

	if item.IsExpired() {
		dc.stats.Misses.Inc()
		// Clean up expired item
		go dc.Delete(key)
		return nil, false
	}

	// Update access statistics
	item.AccessedAt = time.Now()
	item.AccessCount++
	dc.stats.Hits.Inc()

	return item, true
}

// Set stores an item in the cache
func (dc *DistroCache) Set(key string, value interface{}, ttl time.Duration, tags []string) {
	dc.mutex.Lock()
	defer dc.mutex.Unlock()

	// Check if we're at capacity and need to evict
	if len(dc.data) >= dc.config.MaxSize {
		dc.evictLRU()
	}

	// Remove old item from tag index if it exists
	if oldItem, exists := dc.data[key]; exists {
		dc.removeFromTagIndex(key, oldItem.Tags)
	}

	item := &CacheItem{
		Key:         key,
		Value:       value,
		TTL:         ttl,
		CreatedAt:   time.Now(),
		AccessedAt:  time.Now(),
		AccessCount: 1,
		Tags:        tags,
		Metadata:    make(map[string]interface{}),
	}

	dc.data[key] = item
	dc.addToTagIndex(key, tags)
	dc.stats.Sets.Inc()
	dc.stats.TotalItems.Set(float64(len(dc.data)))
}

// Delete removes an item from the cache
func (dc *DistroCache) Delete(key string) bool {
	dc.mutex.Lock()
	defer dc.mutex.Unlock()

	item, exists := dc.data[key]
	if !exists {
		return false
	}

	dc.removeFromTagIndex(key, item.Tags)
	delete(dc.data, key)
	dc.stats.Deletes.Inc()
	dc.stats.TotalItems.Set(float64(len(dc.data)))
	return true
}

// InvalidateByTag removes all items with a specific tag
func (dc *DistroCache) InvalidateByTag(tag string) int {
	dc.mutex.Lock()
	defer dc.mutex.Unlock()

	keys, exists := dc.tagIndex[tag]
	if !exists {
		return 0
	}

	deleted := 0
	for _, key := range keys {
		if item, exists := dc.data[key]; exists {
			dc.removeFromTagIndex(key, item.Tags)
			delete(dc.data, key)
			deleted++
		}
	}

	delete(dc.tagIndex, tag)
	dc.stats.TotalItems.Set(float64(len(dc.data)))
	return deleted
}

// addToTagIndex adds a key to the tag index
func (dc *DistroCache) addToTagIndex(key string, tags []string) {
	for _, tag := range tags {
		dc.tagIndex[tag] = append(dc.tagIndex[tag], key)
	}
}

// removeFromTagIndex removes a key from the tag index
func (dc *DistroCache) removeFromTagIndex(key string, tags []string) {
	for _, tag := range tags {
		keys := dc.tagIndex[tag]
		for i, k := range keys {
			if k == key {
				dc.tagIndex[tag] = append(keys[:i], keys[i+1:]...)
				break
			}
		}
		if len(dc.tagIndex[tag]) == 0 {
			delete(dc.tagIndex, tag)
		}
	}
}

// evictLRU removes the least recently used item
func (dc *DistroCache) evictLRU() {
	var oldestKey string
	var oldestTime time.Time

	for key, item := range dc.data {
		if oldestKey == "" || item.AccessedAt.Before(oldestTime) {
			oldestKey = key
			oldestTime = item.AccessedAt
		}
	}

	if oldestKey != "" {
		if item, exists := dc.data[oldestKey]; exists {
			dc.removeFromTagIndex(oldestKey, item.Tags)
		}
		delete(dc.data, oldestKey)
		dc.stats.Evictions.Inc()
	}
}

// startCleanup starts the background cleanup goroutine
func (dc *DistroCache) startCleanup() {
	ticker := time.NewTicker(dc.config.CleanupInterval)
	defer ticker.Stop()

	for range ticker.C {
		dc.cleanup()
	}
}

// cleanup removes expired items
func (dc *DistroCache) cleanup() {
	dc.mutex.Lock()
	defer dc.mutex.Unlock()

	for key, item := range dc.data {
		if item.IsExpired() {
			dc.removeFromTagIndex(key, item.Tags)
			delete(dc.data, key)
		}
	}
	dc.stats.TotalItems.Set(float64(len(dc.data)))
}

// GetStats returns cache statistics
func (dc *DistroCache) GetStats() map[string]interface{} {
	dc.mutex.RLock()
	defer dc.mutex.RUnlock()

	return map[string]interface{}{
		"total_items": len(dc.data),
		"total_tags":  len(dc.tagIndex),
		"node_id":     dc.config.NodeID,
		"uptime":      time.Since(time.Now()).String(),
	}
}

// HTTP Handlers

func (dc *DistroCache) handleGet(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	key := vars["key"]

	item, found := dc.Get(key)
	if !found {
		http.Error(w, "Key not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(item)
}

func (dc *DistroCache) handleSet(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	key := vars["key"]

	var req struct {
		Value interface{} `json:"value"`
		TTL   int         `json:"ttl,omitempty"`
		Tags  []string    `json:"tags,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	ttl := time.Duration(req.TTL) * time.Second
	if req.TTL == 0 {
		ttl = dc.config.DefaultTTL
	}

	dc.Set(key, req.Value, ttl, req.Tags)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func (dc *DistroCache) handleDelete(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	key := vars["key"]

	deleted := dc.Delete(key)
	if !deleted {
		http.Error(w, "Key not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func (dc *DistroCache) handleInvalidateTag(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	tag := vars["tag"]

	deleted := dc.InvalidateByTag(tag)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"deleted": deleted,
	})
}

func (dc *DistroCache) handleStats(w http.ResponseWriter, r *http.Request) {
	stats := dc.GetStats()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

func (dc *DistroCache) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "healthy",
		"version": "1.0.0",
		"node_id": dc.config.NodeID,
	})
}

// setupRoutes configures HTTP routes
func (dc *DistroCache) setupRoutes() *mux.Router {
	r := mux.NewRouter()

	// API routes
	api := r.PathPrefix("/api/v1").Subrouter()
	api.HandleFunc("/cache/{key}", dc.handleGet).Methods("GET")
	api.HandleFunc("/cache/{key}", dc.handleSet).Methods("POST", "PUT")
	api.HandleFunc("/cache/{key}", dc.handleDelete).Methods("DELETE")
	api.HandleFunc("/invalidate/tag/{tag}", dc.handleInvalidateTag).Methods("POST")
	api.HandleFunc("/stats", dc.handleStats).Methods("GET")
	api.HandleFunc("/health", dc.handleHealth).Methods("GET")

	// Metrics endpoint
	r.Handle("/metrics", promhttp.Handler())

	// Add CORS middleware
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusOK)
				return
			}

			next.ServeHTTP(w, r)
		})
	})

	return r
}

func main() {
	config := &CacheConfig{
		MaxSize:           10000,
		DefaultTTL:        5 * time.Minute,
		CleanupInterval:   1 * time.Minute,
		Port:              8080,
		NodeID:            "node-1",
		ReplicationFactor: 2,
	}

	cache := NewDistroCache(config)
	router := cache.setupRoutes()

	fmt.Printf(" DistroCache Server starting on port %d\n", config.Port)
	fmt.Printf(" Metrics available at http://localhost:%d/metrics\n", config.Port)
	fmt.Printf(" Health check at http://localhost:%d/api/v1/health\n", config.Port)

	log.Fatal(http.ListenAndServe(":"+strconv.Itoa(config.Port), router))
}
