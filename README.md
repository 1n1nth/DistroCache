# DistroCache

A high-performance distributed in-memory cache server written in Go.

## Features

- **In-memory key-value storage** with configurable TTL
- **Tag-based invalidation** for grouped cache entries
- **LRU eviction policy** with configurable maximum size
- **Prometheus metrics integration** for monitoring
- **RESTful HTTP API** with JSON responses
- **Concurrent access** optimized with reader-writer locks
- **Background cleanup** of expired entries
- **Consistent hashing** foundation for distributed deployment

## Quick Start

```bash
go mod tidy
go run maincacheserver.go
```

Server starts on `http://localhost:8080`

## API Endpoints

### Cache Operations
```
GET    /api/v1/cache/{key}           # Retrieve item
POST   /api/v1/cache/{key}           # Store item
PUT    /api/v1/cache/{key}           # Store item
DELETE /api/v1/cache/{key}           # Delete item
```

### Management
```
POST   /api/v1/invalidate/tag/{tag}  # Invalidate by tag
GET    /api/v1/stats                 # Cache statistics
GET    /api/v1/health                # Health check
GET    /metrics                      # Prometheus metrics
```

## Usage Examples

### Store an item
```bash
curl -X POST http://localhost:8080/api/v1/cache/user:123 \
  -H "Content-Type: application/json" \
  -d '{
    "value": {"name": "John", "email": "john@example.com"},
    "ttl": 300,
    "tags": ["user", "session"]
  }'
```

### Retrieve an item
```bash
curl http://localhost:8080/api/v1/cache/user:123
```

### Invalidate by tag
```bash
curl -X POST http://localhost:8080/api/v1/invalidate/tag/user
```

## Configuration

Default configuration in `main()`:

```go
config := &CacheConfig{
    MaxSize:           10000,           // Maximum items
    DefaultTTL:        5 * time.Minute, // Default expiration
    CleanupInterval:   1 * time.Minute, // Cleanup frequency
    Port:              8080,            // HTTP port
    NodeID:            "node-1",        // Node identifier
    ReplicationFactor: 2,               // Replication count
}
```

## Data Structure

Items stored with metadata:
```json
{
  "key": "user:123",
  "value": {"name": "John"},
  "ttl": 300000000000,
  "created_at": "2025-01-15T10:30:00Z",
  "accessed_at": "2025-01-15T10:35:00Z",
  "access_count": 5,
  "tags": ["user", "session"]
}
```

## Monitoring

Prometheus metrics available at `/metrics`:

- `distrocache_hits_total` - Cache hits
- `distrocache_misses_total` - Cache misses  
- `distrocache_sets_total` - Set operations
- `distrocache_deletes_total` - Delete operations
- `distrocache_evictions_total` - LRU evictions
- `distrocache_items_total` - Current item count
- `distrocache_access_duration_seconds` - Access time histogram

## Architecture

- **Thread-safe** operations using `sync.RWMutex`
- **Tag indexing** for efficient bulk operations
- **Consistent hashing** for key distribution
- **Background cleanup** goroutine for expired items
- **LRU eviction** when cache reaches capacity
- **Lazy expiration** during access operations

## Dependencies

```
github.com/gorilla/mux
github.com/prometheus/client_golang/prometheus
```

## Performance Characteristics

- **O(1)** average case for get/set operations
- **O(n)** LRU eviction (linear scan)
- **O(k)** tag invalidation where k = items with tag
- **Concurrent reads** supported via RWMutex
- **Memory usage** proportional to stored items

## Production Notes

- Configure cleanup interval based on TTL patterns
- Monitor eviction rate to size cache appropriately  
- Use consistent node IDs for distributed deployment
- Set appropriate TTL values to balance freshness and performance
- Monitor Prometheus metrics for performance tuning
