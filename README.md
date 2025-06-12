# RoadRunner Sentry Transport Plugin

A high-performance, asynchronous Sentry transport plugin for RoadRunner that eliminates the performance impact of
blocking HTTP requests in PHP applications.

## Features

### Core Functionality

- **Asynchronous Processing**: Events are processed in Go routines, eliminating blocking HTTP requests in PHP
- **Rate Limiting**: Built-in rate limiting based on Sentry response headers (`X-Sentry-Rate-Limits`, `Retry-After`)
- **Retry Mechanism**: Configurable exponential backoff with jitter for failed events
- **Batch Processing**: Efficient batch processing of multiple events
- **Dead Letter Queue**: Optional dead letter queue for permanently failed events
- **Queue Management**: Buffered event queue with configurable workers and batch sizes

### Transport Features

- **HTTP/HTTPS Support**: Full support for both HTTP and HTTPS Sentry endpoints
- **Compression**: Optional gzip compression for payload optimization
- **Connection Pooling**: Efficient HTTP connection reuse

### Monitoring & Observability

- **Metrics**: Comprehensive metrics for sent, failed, and rate-limited events
- **RPC Interface**: Full RPC interface for PHP integration and monitoring

## Installation

### 1. Building with Velox

Create a `velox.toml` configuration file:

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

# Your custom Sentry Transport plugin
sentry_transport = { ref = "1.0.0", owner = "butschster", repository = "rr-sentry-transport" }
```

Build RoadRunner with the plugin:

```bash
vx build -c velox.toml .
```

## Configuration

Add the Sentry Transport configuration to your `.rr.yaml` file:

```yaml
sentry_transport:
  # Enable/disable the plugin
  enabled: true

  # Sentry DSN
  dsn: "https://your-public-key@sentry.io/your-project-id"

  # HTTP transport settings
  transport:
    timeout: "30s"
    connect_timeout: "10s"
    compression: true
    ssl_verify: true
    # Optional proxy settings
    # proxy: "http://proxy.example.com:8080"
    # proxy_auth: "username:password"

  # Retry configuration
  retry:
    max_attempts: 3
    initial_backoff: "1s"
    backoff_multiplier: 2
    max_backoff: "300s"
    dead_letter_queue: true

  # Queue configuration
  queue:
    buffer_size: 1000
    workers: 1
    batch_size: 10
    batch_timeout: "5s"
```

### Configuration Options

#### Transport Settings

- `timeout`: HTTP request timeout
- `connect_timeout`: Connection establishment timeout
- `compression`: Enable gzip compression
- `ssl_verify`: Enable SSL certificate verification
- `proxy`: HTTP proxy URL
- `proxy_auth`: Proxy authentication (username:password)

#### Retry Settings

- `max_attempts`: Maximum retry attempts per event
- `initial_backoff`: Initial backoff duration
- `backoff_multiplier`: Exponential backoff multiplier
- `max_backoff`: Maximum backoff duration
- `dead_letter_queue`: Enable dead letter queue for failed events

#### Queue Settings

- `buffer_size`: Event queue buffer size
- `workers`: Number of worker goroutines
- `batch_size`: Maximum events per batch
- `batch_timeout`: Batch processing timeout

## PHP Integration

### Basic Usage

```php
<?php

declare(strict_types=1);

namespace App\Application;

use Sentry\Event;
use Sentry\Serializer\PayloadSerializerInterface;
use Sentry\Transport\Result;
use Sentry\Transport\ResultStatus;
use Sentry\Transport\TransportInterface;
use Spiral\Goridge\RPC\RPCInterface;

final readonly class RPCTransport implements TransportInterface
{
    public function __construct(
        private RPCInterface $rpc,
        private PayloadSerializerInterface $payloadSerializer,
    ) {}

    public function send(Event $event): Result
    {
        try {
            $response = $this->rpc->call('sentry_transport.SendEvent', [
                'event_id' => (string) $event->getId(),
                'type' => (string) $event->getType(),
                'payload' => $this->payloadSerializer->serialize($event),
            ]);
        } catch (\Throwable $e) {
            return new Result(ResultStatus::failed());
        }

        return new Result(ResultStatus::success());
    }

    public function close(?int $timeout = null): Result
    {
        return new Result(ResultStatus::success());
    }
}
```

