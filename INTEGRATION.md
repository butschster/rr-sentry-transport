# Integration Guide

## Adding Sentry Transport Plugin to RoadRunner

### Step 1: Plugin Registration

Create or modify your `container/plugins.go` file:

```go
package container

import (
    "github.com/roadrunner-server/endure/v2"
    "github.com/roadrunner-server/logger/v5"
    "github.com/roadrunner-server/rpc/v5"
    "github.com/roadrunner-server/server/v5"
    
    // Add your Sentry Transport plugin
    sentryTransport "github.com/your-org/roadrunner-sentry-transport"
    
    "log/slog"
)

// NewContainer creates a new container with all plugins
func NewContainer() (*endure.Endure, error) {
    container := endure.New(slog.LevelDebug)

    // Register core plugins
    container.Register(&logger.Plugin{})
    container.Register(&rpc.Plugin{})
    container.Register(&server.Plugin{})
    
    // Register Sentry Transport plugin
    container.Register(&sentryTransport.Plugin{})

    return container, nil
}
```

### Step 2: Main Application

Create or modify your `main.go` file:

```go
package main

import (
    "context"
    "log/slog"
    "os"
    "time"
    
    "github.com/roadrunner-server/endure/v2"
    "github.com/your-org/your-rr-app/container"
)

func main() {
    // Create container
    cont, err := container.NewContainer()
    if err != nil {
        panic(err)
    }

    // Initialize from config
    if err := cont.Init(); err != nil {
        panic(err)
    }

    // Start all services
    if err := cont.Serve(); err != nil {
        panic(err)
    }

    // Wait for shutdown signal
    cont.Stop(context.Background())
}
```

### Step 3: Go Module Setup

Create or update your `go.mod`:

```go
module github.com/your-org/your-rr-app

go 1.22

require (
    github.com/roadrunner-server/endure/v2 v2.4.4
    github.com/roadrunner-server/errors v1.4.1
    github.com/roadrunner-server/logger/v5 v5.0.2
    github.com/roadrunner-server/rpc/v5 v5.0.2
    github.com/roadrunner-server/server/v5 v5.0.2
    github.com/your-org/roadrunner-sentry-transport v1.0.0
    go.uber.org/zap v1.27.0
)
```

### Step 4: Build and Run

```bash
# Download dependencies
go mod tidy

# Build RoadRunner
go build -o rr main.go

# Run with configuration
./rr serve -c .rr.yaml
```

### Step 5: Verify Installation

Check if the plugin is loaded correctly:

```bash
# Check plugin status via RPC
echo '{"method": "sentry_transport.GetStatus", "params": [true], "id": 1}' | \
  nc localhost 6001
```

## Using Velox (Recommended)

For easier plugin management, use Velox:

### 1. Create velox.toml

```toml
[roadrunner]
ref = "master"

[github]
[github.token]
token = "${RT_TOKEN}"

[github.plugins]
# Core plugins
logger = { ref = "v5.0.2", owner = "roadrunner-server", repository = "logger" }
rpc = { ref = "v5.0.2", owner = "roadrunner-server", repository = "rpc" }
server = { ref = "v5.0.2", owner = "roadrunner-server", repository = "server" }

# Optional HTTP plugin
http = { ref = "v5.0.2", owner = "roadrunner-server", repository = "http" }

# Your Sentry Transport plugin
sentry_transport = { ref = "main", owner = "your-org", repository = "roadrunner-sentry-transport" }
```

### 2. Build with Velox

```bash
# Install Velox
go install github.com/roadrunner-server/velox/v2025/cmd/vx@latest

# Build RoadRunner with your plugin
vx build -c velox.toml -o ./rr
```

## Configuration Examples

### Minimal Configuration

```yaml
sentry_transport:
  enabled: true
  dsn: "https://your-key@sentry.io/project-id"
```

### Production Configuration

```yaml
sentry_transport:
  enabled: true
  dsn: "https://your-key@sentry.io/project-id"

  transport:
    timeout: "30s"
    connect_timeout: "10s"
    compression: true
    ssl_verify: true

  retry:
    max_attempts: 5
    initial_backoff: "2s"
    backoff_multiplier: 2
    max_backoff: "300s"
    dead_letter_queue: true

  queue:
    buffer_size: 2000
    workers: 8
    batch_size: 25
    batch_timeout: "3s"

  logging:
    level: "warn"
```

### Development Configuration

```yaml
sentry_transport:
  enabled: true
  # Leave DSN empty for dry-run mode (events logged but not sent)
  dsn: ""

  queue:
    buffer_size: 100
    workers: 2
    batch_size: 5
    batch_timeout: "1s"

  logging:
    level: "debug"
```

## Troubleshooting

### Plugin Not Loading

1. Check if the plugin is registered in `container/plugins.go`
2. Verify the import path is correct
3. Ensure all dependencies are available (`go mod tidy`)
4. Check RoadRunner logs for initialization errors

### Events Not Being Sent

1. Verify DSN is correct and accessible
2. Check plugin status: `sentry_transport.GetStatus`
3. Review plugin logs for errors
4. Test network connectivity to Sentry

### Performance Issues

1. Monitor queue length and worker utilization
2. Adjust worker count and batch size
3. Check for rate limiting in logs
4. Monitor memory usage and GC pressure

### Common Errors

#### "Plugin disabled or not configured"

- Check if `enabled: true` in configuration
- Verify DSN is provided (unless using dry-run mode)
- Ensure configuration section exists

#### "Queue is full"

- Increase `queue.buffer_size`
- Add more workers if processing is slow
- Check for network issues causing backlog

#### "Rate limited"

- Normal behavior when Sentry rate limits are hit
- Events will be automatically retried
- Consider adjusting event volume or Sentry plan

## Monitoring in Production

### Health Checks

Add health check endpoint:

```go
// In your HTTP handlers
func healthCheck(w http.ResponseWriter, r *http.Request) {
    rpc := getRPCClient() // Your RPC client
    
    status, err := rpc.Call("sentry_transport.GetStatus", true)
    if err != nil {
        http.Error(w, "Plugin unavailable", 503)
        return
    }
    
    if !status["enabled"].(bool) {
        http.Error(w, "Plugin disabled", 503)
        return
    }
    
    w.WriteHeader(200)
    json.NewEncoder(w).Encode(status)
}
```

### Metrics Collection

Integrate with your monitoring system:

```go
func collectMetrics() {
    rpc := getRPCClient()
    
    metrics, err := rpc.Call("sentry_transport.GetMetrics", true)
    if err != nil {
        log.Printf("Failed to get metrics: %v", err)
        return
    }
    
    // Send to your metrics system (Prometheus, DataDog, etc.)
    prometheus.EventsSentCounter.Add(float64(metrics["events_sent"].(int64)))
    prometheus.EventsFailedCounter.Add(float64(metrics["events_failed"].(int64)))
    // ... other metrics
}
```

## Advanced Usage

### Custom Event Types

The plugin supports any Sentry event type:

```php
// Transaction event
$event = [
    'event_id' => uniqid(),
    'type' => 'transaction',
    'payload' => json_encode([
        'transaction' => 'GET /api/users',
        'start_timestamp' => $startTime,
        'timestamp' => $endTime,
        // ... other transaction data
    ])
];

// Session event
$event = [
    'event_id' => uniqid(),
    'type' => 'session',
    'payload' => json_encode([
        'sid' => session_id(),
        'status' => 'ok',
        'started' => time() - 3600,
        // ... other session data
    ])
];
```

### Integration with Existing Sentry SDK

You can use this plugin alongside the official Sentry PHP SDK:

```php
// Configure Sentry SDK to use null transport
Sentry\init([
    'dsn' => null, // Disable direct transmission
    'transport' => new NullTransport(),
]);

// Override the transport in your error handler
class CustomSentryTransport implements TransportInterface
{
    private SentryTransportClient $client;
    
    public function __construct()
    {
        $this->client = new SentryTransportClient();
    }
    
    public function send(Event $event): Result
    {
        $serialized = serialize($event); // Use Sentry's serializer
        
        $result = $this->client->sendEvent([
            'event_id' => (string) $event->getId(),
            'type' => 'event',
            'payload' => $serialized
        ]);
        
        return new Result(
            $result['success'] ? ResultStatus::success() : ResultStatus::failed(),
            $event
        );
    }
}
```

This integration guide provides everything needed to successfully integrate and deploy the Sentry Transport plugin in a
production RoadRunner environment.
