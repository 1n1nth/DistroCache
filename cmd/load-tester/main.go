package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// TestResult represents the result of a single test
type TestResult struct {
	StatusCode  int
	Duration    time.Duration
	CacheStatus string
	Error       error
	RequestType string
}

// LoadTester performs load testing against the cache system
type LoadTester struct {
	CacheURL string
	AppURL   string
	Client   *http.Client
	Results  []TestResult
	mutex    sync.Mutex
}

// NewLoadTester creates a new load tester
func NewLoadTester(cacheURL, appURL string) *LoadTester {
	return &LoadTester{
		CacheURL: cacheURL,
		AppURL:   appURL,
		Client:   &http.Client{Timeout: 10 * time.Second},
		Results:  make([]TestResult, 0),
	}
}

// DirectCacheTest tests the cache server directly
func (lt *LoadTester) DirectCacheTest(concurrency, requests int) {
	fmt.Printf("🚀 Running direct cache test: %d concurrent workers, %d total requests\n", concurrency, requests)

	var wg sync.WaitGroup
	var completed int64
	requestsPerWorker := requests / concurrency

	startTime := time.Now()

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for j := 0; j < requestsPerWorker; j++ {
				// Test SET operation
				key := fmt.Sprintf("test:worker:%d:req:%d", workerID, j)
				value := map[string]interface{}{
					"worker_id":  workerID,
					"request_id": j,
					"timestamp":  time.Now().Unix(),
					"data":       fmt.Sprintf("Test data for worker %d request %d", workerID, j),
				}

				result := lt.setCacheValue(key, value, 60, []string{"load-test", fmt.Sprintf("worker-%d", workerID)})
				lt.addResult(result)

				// Test GET operation
				result = lt.getCacheValue(key)
				result.RequestType = "GET"
				lt.addResult(result)

				atomic.AddInt64(&completed, 1)
				if completed%100 == 0 {
					fmt.Printf("Completed: %d/%d requests\n", completed, int64(requests*2))
				}
			}
		}(i)
	}

	wg.Wait()
	totalDuration := time.Since(startTime)

	lt.printResults("Direct Cache Test", totalDuration, requests*2)
}

// ApplicationTest tests through the sample application
func (lt *LoadTester) ApplicationTest(concurrency, requests int) {
	fmt.Printf("🌐 Running application test: %d concurrent workers, %d total requests\n", concurrency, requests)

	var wg sync.WaitGroup
	var completed int64
	requestsPerWorker := requests / concurrency

	startTime := time.Now()

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for j := 0; j < requestsPerWorker; j++ {
				// Randomly test different users (1-5)
				userID := (j % 5) + 1

				// Test user endpoint
				result := lt.getUser(userID)
				lt.addResult(result)

				// Test products endpoint
				categories := []string{"Electronics", "Sports", "Appliances", "Books"}
				category := categories[j%len(categories)]
				result = lt.getProducts(category)
				lt.addResult(result)

				atomic.AddInt64(&completed, 1)
				if completed%50 == 0 {
					fmt.Printf("Completed: %d/%d requests\n", completed, int64(requests*2))
				}
			}
		}(i)
	}

	wg.Wait()
	totalDuration := time.Since(startTime)

	lt.printResults("Application Test", totalDuration, requests*2)
}

// MixedWorkloadTest simulates a realistic mixed workload
func (lt *LoadTester) MixedWorkloadTest(duration time.Duration, concurrency int) {
	fmt.Printf("⚡ Running mixed workload test: %d workers for %v\n", concurrency, duration)

	var wg sync.WaitGroup
	var totalRequests int64
	stopTime := time.Now().Add(duration)

	startTime := time.Now()

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			requestCount := 0

			for time.Now().Before(stopTime) {
				// Mix of operations with realistic weights
				switch requestCount % 10 {
				case 0, 1, 2, 3, 4: // 50% - Read users (most common)
					userID := (requestCount % 5) + 1
					result := lt.getUser(userID)
					lt.addResult(result)
				case 5, 6, 7: // 30% - Read products
					categories := []string{"Electronics", "Sports", "all"}
					category := categories[requestCount%len(categories)]
					result := lt.getProducts(category)
					lt.addResult(result)
				case 8: // 10% - Update user (invalidates cache)
					userID := (requestCount % 5) + 1
					result := lt.updateUser(userID)
					lt.addResult(result)
				case 9: // 10% - Direct cache operations
					key := fmt.Sprintf("mixed:worker:%d:req:%d", workerID, requestCount)
					value := map[string]interface{}{
						"type":    "mixed_workload",
						"worker":  workerID,
						"request": requestCount,
					}
					result := lt.setCacheValue(key, value, 30, []string{"mixed-test"})
					lt.addResult(result)
				}

				requestCount++
				atomic.AddInt64(&totalRequests, 1)

				// Small delay to simulate realistic usage
				time.Sleep(time.Millisecond * 10)
			}
		}(i)
	}

	wg.Wait()
	totalDuration := time.Since(startTime)

	lt.printResults("Mixed Workload Test", totalDuration, int(totalRequests))
}

// Helper methods for different request types

func (lt *LoadTester) setCacheValue(key string, value interface{}, ttl int, tags []string) TestResult {
	reqBody := map[string]interface{}{
		"value": value,
		"ttl":   ttl,
		"tags":  tags,
	}

	jsonData, _ := json.Marshal(reqBody)
	start := time.Now()

	resp, err := lt.Client.Post(
		fmt.Sprintf("%s/api/v1/cache/%s", lt.CacheURL, key),
		"application/json",
		bytes.NewBuffer(jsonData),
	)

	duration := time.Since(start)

	result := TestResult{
		Duration:    duration,
		Error:       err,
		RequestType: "SET",
	}

	if resp != nil {
		result.StatusCode = resp.StatusCode
		resp.Body.Close()
	}

	return result
}

func (lt *LoadTester) getCacheValue(key string) TestResult {
	start := time.Now()
	resp, err := lt.Client.Get(fmt.Sprintf("%s/api/v1/cache/%s", lt.CacheURL, key))
	duration := time.Since(start)

	result := TestResult{
		Duration:    duration,
		Error:       err,
		RequestType: "GET",
	}

	if resp != nil {
		result.StatusCode = resp.StatusCode
		resp.Body.Close()
	}

	return result
}

func (lt *LoadTester) getUser(userID int) TestResult {
	start := time.Now()
	resp, err := lt.Client.Get(fmt.Sprintf("%s/api/users/%d", lt.AppURL, userID))
	duration := time.Since(start)

	result := TestResult{
		Duration:    duration,
		Error:       err,
		RequestType: "GET_USER",
	}

	if resp != nil {
		result.StatusCode = resp.StatusCode
		result.CacheStatus = resp.Header.Get("X-Cache")
		resp.Body.Close()
	}

	return result
}

func (lt *LoadTester) getProducts(category string) TestResult {
	start := time.Now()
	resp, err := lt.Client.Get(fmt.Sprintf("%s/api/products?category=%s", lt.AppURL, category))
	duration := time.Since(start)

	result := TestResult{
		Duration:    duration,
		Error:       err,
		RequestType: "GET_PRODUCTS",
	}

	if resp != nil {
		result.StatusCode = resp.StatusCode
		result.CacheStatus = resp.Header.Get("X-Cache")
		resp.Body.Close()
	}

	return result
}

func (lt *LoadTester) updateUser(userID int) TestResult {
	reqBody := map[string]string{
		"name":  fmt.Sprintf("Updated User %d", userID),
		"email": fmt.Sprintf("updated%d@example.com", userID),
	}

	jsonData, _ := json.Marshal(reqBody)
	start := time.Now()

	resp, err := lt.Client.Post(
		fmt.Sprintf("%s/api/users/%d/update", lt.AppURL, userID),
		"application/json",
		bytes.NewBuffer(jsonData),
	)

	duration := time.Since(start)

	result := TestResult{
		Duration:    duration,
		Error:       err,
		RequestType: "UPDATE_USER",
	}

	if resp != nil {
		result.StatusCode = resp.StatusCode
		resp.Body.Close()
	}

	return result
}

func (lt *LoadTester) addResult(result TestResult) {
	lt.mutex.Lock()
	defer lt.mutex.Unlock()
	lt.Results = append(lt.Results, result)
}

func (lt *LoadTester) printResults(testName string, totalDuration time.Duration, totalRequests int) {
	lt.mutex.Lock()
	defer lt.mutex.Unlock()
	str := strings.Repeat("=", 60)
	fmt.Printf("\n %v \n", str)
	fmt.Printf(" %s Results\n", testName)
	fmt.Printf("%v\n", str)

	if len(lt.Results) == 0 {
		fmt.Println("No results to display")
		return
	}

	// Calculate statistics
	var totalTime time.Duration
	var successCount, errorCount int
	var cacheHits, cacheMisses int
	statusCodes := make(map[int]int)
	requestTypes := make(map[string]int)

	var minDuration, maxDuration time.Duration
	durations := make([]time.Duration, 0, len(lt.Results))

	for i, result := range lt.Results {
		if i == 0 {
			minDuration = result.Duration
			maxDuration = result.Duration
		}

		totalTime += result.Duration
		durations = append(durations, result.Duration)

		if result.Duration < minDuration {
			minDuration = result.Duration
		}
		if result.Duration > maxDuration {
			maxDuration = result.Duration
		}

		if result.Error == nil {
			successCount++
		} else {
			errorCount++
		}

		statusCodes[result.StatusCode]++
		requestTypes[result.RequestType]++

		if result.CacheStatus == "HIT" {
			cacheHits++
		} else if result.CacheStatus == "MISS" {
			cacheMisses++
		}
	}

	avgDuration := totalTime / time.Duration(len(lt.Results))
	rps := float64(totalRequests) / totalDuration.Seconds()

	// Print summary
	fmt.Printf("Total Duration:    %v\n", totalDuration)
	fmt.Printf("Total Requests:    %d\n", totalRequests)
	fmt.Printf("Requests/Second:   %.2f\n", rps)
	fmt.Printf("Success Rate:      %.2f%% (%d/%d)\n", float64(successCount)/float64(len(lt.Results))*100, successCount, len(lt.Results))
	fmt.Printf("Error Rate:        %.2f%% (%d/%d)\n", float64(errorCount)/float64(len(lt.Results))*100, errorCount, len(lt.Results))

	if cacheHits+cacheMisses > 0 {
		fmt.Printf("Cache Hit Rate:    %.2f%% (%d/%d)\n", float64(cacheHits)/float64(cacheHits+cacheMisses)*100, cacheHits, cacheHits+cacheMisses)
	}

	fmt.Printf("\nResponse Times:\n")
	fmt.Printf("  Min:             %v\n", minDuration)
	fmt.Printf("  Max:             %v\n", maxDuration)
	fmt.Printf("  Average:         %v\n", avgDuration)

	// Calculate percentiles
	if len(durations) > 0 {
		// Simple percentile calculation (not perfectly accurate but good enough)
		p50 := durations[len(durations)*50/100]
		p95 := durations[len(durations)*95/100]
		p99 := durations[len(durations)*99/100]

		fmt.Printf("  50th percentile: %v\n", p50)
		fmt.Printf("  95th percentile: %v\n", p95)
		fmt.Printf("  99th percentile: %v\n", p99)
	}

	fmt.Printf("\nStatus Code Distribution:\n")
	for code, count := range statusCodes {
		fmt.Printf("  %d: %d (%.1f%%)\n", code, count, float64(count)/float64(len(lt.Results))*100)
	}

	fmt.Printf("\nRequest Type Distribution:\n")
	for reqType, count := range requestTypes {
		fmt.Printf("  %s: %d (%.1f%%)\n", reqType, count, float64(count)/float64(len(lt.Results))*100)
	}

	// Clear results for next test
	lt.Results = make([]TestResult, 0)
}

func main() {
	var (
		cacheURL    = flag.String("cache", "http://localhost:8080", "Cache server URL")
		appURL      = flag.String("app", "http://localhost:3000", "Application server URL")
		testType    = flag.String("test", "mixed", "Test type: direct, app, mixed, all")
		concurrency = flag.Int("c", 10, "Number of concurrent workers")
		requests    = flag.Int("r", 1000, "Number of requests for direct/app tests")
		duration    = flag.Duration("d", 60*time.Second, "Duration for mixed workload test")
	)
	flag.Parse()

	fmt.Println("DistroCache Load Tester")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("Cache URL: %s\n", *cacheURL)
	fmt.Printf("App URL: %s\n", *appURL)
	fmt.Printf("Test Type: %s\n", *testType)
	fmt.Printf("Concurrency: %d\n", *concurrency)
	fmt.Printf("Requests: %d\n", *requests)
	fmt.Printf("Duration: %v\n", *duration)
	fmt.Println()

	tester := NewLoadTester(*cacheURL, *appURL)

	switch *testType {
	case "direct":
		tester.DirectCacheTest(*concurrency, *requests)
	case "app":
		tester.ApplicationTest(*concurrency, *requests)
	case "mixed":
		tester.MixedWorkloadTest(*duration, *concurrency)
	case "all":
		fmt.Println("Running all test types...")
		tester.DirectCacheTest(*concurrency, *requests/2)
		time.Sleep(2 * time.Second)
		tester.ApplicationTest(*concurrency, *requests/2)
		time.Sleep(2 * time.Second)
		tester.MixedWorkloadTest(*duration/2, *concurrency)
	default:
		log.Fatal("Invalid test type. Use: direct, app, mixed, or all")
	}

	fmt.Println("\nLoad testing completed!")
}
