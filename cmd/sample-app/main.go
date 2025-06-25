package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	_ "github.com/mattn/go-sqlite3"
)

// User represents a user in our mock database
type User struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Email   string `json:"email"`
	Created string `json:"created"`
}

// Product represents a product in our mock database
type Product struct {
	ID       int     `json:"id"`
	Name     string  `json:"name"`
	Price    float64 `json:"price"`
	Category string  `json:"category"`
}

// CacheClient handles communication with DistroCache
type CacheClient struct {
	BaseURL string
	Client  *http.Client
}

// NewCacheClient creates a new cache client
func NewCacheClient(baseURL string) *CacheClient {
	return &CacheClient{
		BaseURL: baseURL,
		Client:  &http.Client{Timeout: 5 * time.Second},
	}
}

// Get retrieves a value from cache
func (c *CacheClient) Get(key string) (interface{}, error) {
	resp, err := c.Client.Get(fmt.Sprintf("%s/api/v1/cache/%s", c.BaseURL, key))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("key not found")
	}

	var result struct {
		Value interface{} `json:"value"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return result.Value, nil
}

// Set stores a value in cache
func (c *CacheClient) Set(key string, value interface{}, ttl int, tags []string) error {
	reqBody := map[string]interface{}{
		"value": value,
		"ttl":   ttl,
		"tags":  tags,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	resp, err := c.Client.Post(
		fmt.Sprintf("%s/api/v1/cache/%s", c.BaseURL, key),
		"application/json",
		bytes.NewBuffer(jsonData),
	)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

// InvalidateTag invalidates all cached items with a specific tag
func (c *CacheClient) InvalidateTag(tag string) error {
	req, err := http.NewRequest("POST", fmt.Sprintf("%s/api/v1/invalidate/tag/%s", c.BaseURL, tag), nil)
	if err != nil {
		return err
	}

	resp, err := c.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

// TestApp represents our sample application
type TestApp struct {
	db    *sql.DB
	cache *CacheClient
}

// NewTestApp creates a new test application
func NewTestApp() *TestApp {
	// Setup SQLite database
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		log.Fatal(err)
	}

	// Create tables
	_, err = db.Exec(`
		CREATE TABLE users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			email TEXT NOT NULL,
			created DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		
		CREATE TABLE products (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			price REAL NOT NULL,
			category TEXT NOT NULL
		);
	`)
	if err != nil {
		log.Fatal(err)
	}

	// Insert sample data
	users := []struct{ name, email string }{
		{"Alice Johnson", "alice@example.com"},
		{"Bob Smith", "bob@example.com"},
		{"Carol Davis", "carol@example.com"},
		{"David Wilson", "david@example.com"},
		{"Eva Brown", "eva@example.com"},
	}

	for _, user := range users {
		_, err = db.Exec("INSERT INTO users (name, email) VALUES (?, ?)", user.name, user.email)
		if err != nil {
			log.Fatal(err)
		}
	}

	products := []struct {
		name     string
		price    float64
		category string
	}{
		{"Laptop Pro", 1299.99, "Electronics"},
		{"Wireless Headphones", 199.99, "Electronics"},
		{"Coffee Maker", 89.99, "Appliances"},
		{"Running Shoes", 129.99, "Sports"},
		{"Smartphone", 699.99, "Electronics"},
		{"Desk Chair", 299.99, "Furniture"},
		{"Water Bottle", 24.99, "Sports"},
		{"Book: Go Programming", 39.99, "Books"},
	}

	for _, product := range products {
		_, err = db.Exec("INSERT INTO products (name, price, category) VALUES (?, ?, ?)",
			product.name, product.price, product.category)
		if err != nil {
			log.Fatal(err)
		}
	}

	return &TestApp{
		db:    db,
		cache: NewCacheClient("http://localhost:8080"),
	}
}

// getUser retrieves a user by ID with caching
func (app *TestApp) getUser(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	userID := vars["id"]
	cacheKey := fmt.Sprintf("user:%s", userID)

	// Try cache first
	start := time.Now()
	if cachedUser, err := app.cache.Get(cacheKey); err == nil {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Cache", "HIT")
		w.Header().Set("X-Response-Time", time.Since(start).String())
		json.NewEncoder(w).Encode(cachedUser)
		return
	}

	// Cache miss - query database
	var user User
	err := app.db.QueryRow("SELECT id, name, email, created FROM users WHERE id = ?", userID).
		Scan(&user.ID, &user.Name, &user.Email, &user.Created)

	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "User not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Cache the result for 5 minutes with user tag
	app.cache.Set(cacheKey, user, 300, []string{"users", fmt.Sprintf("user:%d", user.ID)})

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Cache", "MISS")
	w.Header().Set("X-Response-Time", time.Since(start).String())
	json.NewEncoder(w).Encode(user)
}

// getProducts retrieves products by category with caching
func (app *TestApp) getProducts(w http.ResponseWriter, r *http.Request) {
	category := r.URL.Query().Get("category")
	if category == "" {
		category = "all"
	}

	cacheKey := fmt.Sprintf("products:category:%s", category)

	// Try cache first
	start := time.Now()
	if cachedProducts, err := app.cache.Get(cacheKey); err == nil {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Cache", "HIT")
		w.Header().Set("X-Response-Time", time.Since(start).String())
		json.NewEncoder(w).Encode(cachedProducts)
		return
	}

	// Cache miss - query database
	var query string
	var args []interface{}

	if category == "all" {
		query = "SELECT id, name, price, category FROM products ORDER BY name"
	} else {
		query = "SELECT id, name, price, category FROM products WHERE category = ? ORDER BY name"
		args = []interface{}{category}
	}

	rows, err := app.db.Query(query, args...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var products []Product
	for rows.Next() {
		var product Product
		err := rows.Scan(&product.ID, &product.Name, &product.Price, &product.Category)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		products = append(products, product)
	}

	// Cache the result for 10 minutes with products tag
	tags := []string{"products", fmt.Sprintf("category:%s", category)}
	app.cache.Set(cacheKey, products, 600, tags)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Cache", "MISS")
	w.Header().Set("X-Response-Time", time.Since(start).String())
	json.NewEncoder(w).Encode(products)
}

// updateUser updates a user and invalidates cache
func (app *TestApp) updateUser(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	userID := vars["id"]

	var updateReq struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	}

	if err := json.NewDecoder(r.Body).Decode(&updateReq); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Update database
	_, err := app.db.Exec("UPDATE users SET name = ?, email = ? WHERE id = ?",
		updateReq.Name, updateReq.Email, userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Invalidate user-specific cache
	userIDInt, _ := strconv.Atoi(userID)
	app.cache.InvalidateTag(fmt.Sprintf("user:%d", userIDInt))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

// loadTest performs a simple load test
func (app *TestApp) loadTest(w http.ResponseWriter, r *http.Request) {
	iterations := 100
	results := make([]map[string]interface{}, 0)

	for i := 0; i < iterations; i++ {
		userID := (i % 5) + 1 // Cycle through users 1-5
		start := time.Now()

		resp, err := http.Get(fmt.Sprintf("http://localhost:3000/api/users/%d", userID))
		if err != nil {
			continue
		}

		duration := time.Since(start)
		cacheStatus := resp.Header.Get("X-Cache")
		resp.Body.Close()

		results = append(results, map[string]interface{}{
			"iteration":    i + 1,
			"user_id":      userID,
			"duration_ms":  duration.Milliseconds(),
			"cache_status": cacheStatus,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"total_requests": iterations,
		"results":        results,
	})
}

// benchmarkHandler provides cache performance metrics
func (app *TestApp) benchmarkHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintf(w, `
<!DOCTYPE html>
<html>
<head>
    <title>DistroCache Benchmark Dashboard</title>
    <style>
        body { font-family: Arial, sans-serif; margin: 20px; background: #f5f5f5; }
        .container { max-width: 1200px; margin: 0 auto; }
        .card { background: white; padding: 20px; margin: 10px 0; border-radius: 8px; box-shadow: 0 2px 4px rgba(0,0,0,0.1); }
        .metrics { display: grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); gap: 15px; }
        .metric { text-align: center; padding: 15px; background: #f8f9fa; border-radius: 6px; }
        .metric h3 { margin: 0; color: #333; }
        .metric .value { font-size: 24px; font-weight: bold; color: #007bff; }
        .btn { padding: 10px 20px; margin: 5px; background: #007bff; color: white; border: none; border-radius: 4px; cursor: pointer; }
        .btn:hover { background: #0056b3; }
        .results { margin-top: 20px; }
        #loadResults { background: #f8f9fa; padding: 15px; border-radius: 6px; overflow-x: auto; }
        .status-hit { color: #28a745; font-weight: bold; }
        .status-miss { color: #dc3545; font-weight: bold; }
    </style>
</head>
<body>
    <div class="container">
        <h1>🚀 DistroCache Performance Dashboard</h1>
        
        <div class="card">
            <h2>Quick Tests</h2>
            <button class="btn" onclick="testCacheHit()">Test Cache Hit</button>
            <button class="btn" onclick="testCacheMiss()">Test Cache Miss</button>
            <button class="btn" onclick="runLoadTest()">Run Load Test (100 requests)</button>
            <button class="btn" onclick="invalidateCache()">Invalidate User Cache</button>
            <button class="btn" onclick="showCacheStats()">Show Cache Stats</button>
        </div>

        <div class="card">
            <h2>Live Metrics</h2>
            <div class="metrics" id="metrics">
                <div class="metric">
                    <h3>Cache Hit Rate</h3>
                    <div class="value" id="hitRate">--</div>
                </div>
                <div class="metric">
                    <h3>Avg Response Time</h3>
                    <div class="value" id="avgTime">-- ms</div>
                </div>
                <div class="metric">
                    <h3>Total Requests</h3>
                    <div class="value" id="totalReqs">--</div>
                </div>
                <div class="metric">
                    <h3>Cache Items</h3>
                    <div class="value" id="cacheItems">--</div>
                </div>
            </div>
        </div>

        <div class="card results">
            <h2>Test Results</h2>
            <div id="loadResults">Click "Run Load Test" to see results...</div>
        </div>
    </div>

    <script>
        let testStats = { hits: 0, misses: 0, totalTime: 0 };

        async function testCacheHit() {
            const start = Date.now();
            const response = await fetch('/api/users/1');
            const duration = Date.now() - start;
            const cacheStatus = response.headers.get('X-Cache');
            
            if (cacheStatus === 'HIT') testStats.hits++;
            else testStats.misses++;
            testStats.totalTime += duration;
            
            updateMetrics();
            document.getElementById('loadResults').innerHTML = 
                '✅ Single request completed: ' + duration + 'ms (' + cacheStatus + ')';
        }

        async function testCacheMiss() {
            await fetch('/api/users/1/update', {
                method: 'POST',
                headers: {'Content-Type': 'application/json'},
                body: JSON.stringify({name: 'Alice Updated', email: 'alice.updated@example.com'})
            });
            
            const start = Date.now();
            const response = await fetch('/api/users/1');
            const duration = Date.now() - start;
            const cacheStatus = response.headers.get('X-Cache');
            
            if (cacheStatus === 'HIT') testStats.hits++;
            else testStats.misses++;
            testStats.totalTime += duration;
            
            updateMetrics();
            document.getElementById('loadResults').innerHTML = 
                '✅ Cache miss test completed: ' + duration + 'ms (' + cacheStatus + ')';
        }

        async function runLoadTest() {
            document.getElementById('loadResults').innerHTML = '🔄 Running load test...';
            
            const response = await fetch('/api/load-test');
            const data = await response.json();
            
            let html = '<h3>Load Test Results (' + data.total_requests + ' requests)</h3>';
            html += '<table style="width: 100%%; border-collapse: collapse;">';
            html += '<tr style="background: #e9ecef;"><th>Request</th><th>User ID</th><th>Duration (ms)</th><th>Cache Status</th></tr>';
            
            let totalHits = 0, totalMisses = 0, totalTime = 0;
            
            data.results.forEach(result => {
                const statusClass = result.cache_status === 'HIT' ? 'status-hit' : 'status-miss';
                html += '<tr style="border-bottom: 1px solid #ddd;">';
                html += '<td>' + result.iteration + '</td>';
                html += '<td>' + result.user_id + '</td>';
                html += '<td>' + result.duration_ms + '</td>';
                html += '<td class="' + statusClass + '">' + result.cache_status + '</td>';
                html += '</tr>';
                
                if (result.cache_status === 'HIT') totalHits++;
                else totalMisses++;
                totalTime += result.duration_ms;
            });
            
            html += '</table>';
            html += '<div style="margin-top: 15px; padding: 10px; background: #d4edda; border-radius: 4px;">';
            html += '<strong>Summary:</strong> ';
            html += 'Hit Rate: ' + ((totalHits / (totalHits + totalMisses)) * 100).toFixed(1) + '%%, ';
            html += 'Avg Response Time: ' + (totalTime / data.total_requests).toFixed(1) + 'ms';
            html += '</div>';
            
            testStats.hits += totalHits;
            testStats.misses += totalMisses;
            testStats.totalTime += totalTime;
            
            updateMetrics();
            document.getElementById('loadResults').innerHTML = html;
        }

        async function invalidateCache() {
            await fetch('http://localhost:8080/api/v1/invalidate/tag/users', {method: 'POST'});
            document.getElementById('loadResults').innerHTML = '✅ User cache invalidated';
        }

        async function showCacheStats() {
            const response = await fetch('http://localhost:8080/api/v1/stats');
            const stats = await response.json();
            
            let html = '<h3>Cache Server Statistics</h3>';
            html += '<pre>' + JSON.stringify(stats, null, 2) + '</pre>';
            
            document.getElementById('loadResults').innerHTML = html;
            document.getElementById('cacheItems').textContent = stats.total_items;
        }

        function updateMetrics() {
            const total = testStats.hits + testStats.misses;
            const hitRate = total > 0 ? ((testStats.hits / total) * 100).toFixed(1) + '%%' : '--';
            const avgTime = total > 0 ? (testStats.totalTime / total).toFixed(1) : '--';
            
            document.getElementById('hitRate').textContent = hitRate;
            document.getElementById('avgTime').textContent = avgTime + ' ms';
            document.getElementById('totalReqs').textContent = total;
        }

        setInterval(async () => {
            try {
                const response = await fetch('http://localhost:8080/api/v1/stats');
                const stats = await response.json();
                document.getElementById('cacheItems').textContent = stats.total_items;
            } catch (e) {
                console.log('Cache server not available');
            }
        }, 30000);
    </script>
</body>
</html>
`)

}

func main() {
	app := NewTestApp()
	defer app.db.Close()

	r := mux.NewRouter()

	// API routes
	api := r.PathPrefix("/api").Subrouter()
	api.HandleFunc("/users/{id}", app.getUser).Methods("GET")
	api.HandleFunc("/users/{id}/update", app.updateUser).Methods("POST")
	api.HandleFunc("/products", app.getProducts).Methods("GET")
	api.HandleFunc("/load-test", app.loadTest).Methods("GET")

	// Dashboard
	r.HandleFunc("/", app.benchmarkHandler).Methods("GET")
	r.HandleFunc("/benchmark", app.benchmarkHandler).Methods("GET")

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

	fmt.Println("🌐 Sample Web Application starting on port 3000")
	fmt.Println("📊 Benchmark Dashboard: http://localhost:3000/benchmark")
	fmt.Println("🔗 API Examples:")
	fmt.Println("   GET  http://localhost:3000/api/users/1")
	fmt.Println("   GET  http://localhost:3000/api/products?category=Electronics")
	fmt.Println("   POST http://localhost:3000/api/users/1/update")

	log.Fatal(http.ListenAndServe(":3000", r))
}
