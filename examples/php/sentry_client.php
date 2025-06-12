<?php

declare(strict_types=1);

/**
 * PHP integration example for RoadRunner Sentry Transport plugin
 *
 * This demonstrates how to send Sentry events asynchronously through
 * the RoadRunner Sentry Transport plugin via RPC calls.
 */

use Spiral\RoadRunner\Environment;
use Spiral\RoadRunner\RPC\RPC;

class SentryTransportClient
{
    private RPC $rpc;
    private bool $enabled;

    public function __construct(string $rpcAddress = 'tcp://127.0.0.1:6001')
    {
        $this->rpc = RPC::create($rpcAddress);
        $this->enabled = $this->checkPluginStatus();
    }

    /**
     * Send a single Sentry event asynchronously
     */
    public function sendEvent(array $eventData): array
    {
        if (!$this->enabled) {
            return [
                'success' => false,
                'event_id' => $eventData['event_id'] ?? '',
                'error' => 'Sentry transport plugin is not enabled',
            ];
        }

        $event = [
            'event_id' => $eventData['event_id'] ?? uniqid('sentry_', true),
            'type' => $eventData['type'] ?? 'event',
            'payload' => json_encode($eventData, JSON_THROW_ON_ERROR),
        ];

        try {
            return $this->rpc->call('sentry_transport.SendEvent', $event);
        } catch (Throwable $e) {
            return [
                'success' => false,
                'event_id' => $event['event_id'],
                'error' => $e->getMessage(),
            ];
        }
    }

    /**
     * Send multiple Sentry events in a batch
     */
    public function sendBatch(array $events): array
    {
        if (!$this->enabled) {
            return array_map(fn($event) => [
                'success' => false,
                'event_id' => $event['event_id'] ?? '',
                'error' => 'Sentry transport plugin is not enabled',
            ], $events);
        }

        $formattedEvents = array_map(function ($eventData) {
            return [
                'event_id' => $eventData['event_id'] ?? uniqid('sentry_', true),
                'type' => $eventData['type'] ?? 'event',
                'payload' => json_encode($eventData, JSON_THROW_ON_ERROR),
            ];
        }, $events);

        try {
            return $this->rpc->call('sentry_transport.SendBatch', $formattedEvents);
        } catch (Throwable $e) {
            return array_map(fn($event) => [
                'success' => false,
                'event_id' => $event['event_id'],
                'error' => $e->getMessage(),
            ], $formattedEvents);
        }
    }

    /**
     * Get plugin status and metrics
     */
    public function getStatus(): array
    {
        try {
            return $this->rpc->call('sentry_transport.GetStatus', true);
        } catch (Throwable $e) {
            return [
                'enabled' => false,
                'error' => $e->getMessage(),
            ];
        }
    }

    /**
     * Get detailed metrics
     */
    public function getMetrics(): array
    {
        try {
            return $this->rpc->call('sentry_transport.GetMetrics', true);
        } catch (Throwable $e) {
            return [
                'events_sent' => 0,
                'events_failed' => 0,
                'events_rate_limit' => 0,
                'queue_length' => 0,
                'total_retries' => 0,
                'error' => $e->getMessage(),
            ];
        }
    }

    /**
     * Check if the plugin is enabled and available
     */
    private function checkPluginStatus(): bool
    {
        try {
            $status = $this->getStatus();
            return $status['enabled'] ?? false;
        } catch (Throwable) {
            return false;
        }
    }

    /**
     * Create a Sentry error event
     */
    public static function createErrorEvent(
        Throwable $exception,
        array $context = [],
        string $level = 'error'
    ): array {
        return [
            'event_id' => uniqid('error_', true),
            'type' => 'event',
            'timestamp' => time(),
            'level' => $level,
            'message' => $exception->getMessage(),
            'exception' => [
                'values' => [
                    [
                        'type' => get_class($exception),
                        'value' => $exception->getMessage(),
                        'stacktrace' => [
                            'frames' => self::formatStackTrace($exception->getTrace()),
                        ],
                    ],
                ],
            ],
            'tags' => $context['tags'] ?? [],
            'extra' => $context['extra'] ?? [],
            'environment' => Environment::get('APP_ENV', 'production'),
            'server_name' => gethostname(),
            'platform' => 'php',
            'sdk' => [
                'name' => 'roadrunner-sentry-transport',
                'version' => '1.0.0',
            ],
        ];
    }

    /**
     * Create a Sentry message event
     */
    public static function createMessageEvent(
        string $message,
        array $context = [],
        string $level = 'info'
    ): array {
        return [
            'event_id' => uniqid('msg_', true),
            'type' => 'event',
            'timestamp' => time(),
            'level' => $level,
            'message' => [
                'message' => $message,
                'formatted' => $message,
            ],
            'tags' => $context['tags'] ?? [],
            'extra' => $context['extra'] ?? [],
            'environment' => Environment::get('APP_ENV', 'production'),
            'server_name' => gethostname(),
            'platform' => 'php',
            'sdk' => [
                'name' => 'roadrunner-sentry-transport',
                'version' => '1.0.0',
            ],
        ];
    }

    /**
     * Create a Sentry transaction event
     */
    public static function createTransactionEvent(
        string $transactionName,
        float $startTime,
        float $endTime,
        array $context = []
    ): array {
        return [
            'event_id' => uniqid('txn_', true),
            'type' => 'transaction',
            'timestamp' => $endTime,
            'start_timestamp' => $startTime,
            'transaction' => $transactionName,
            'tags' => $context['tags'] ?? [],
            'extra' => $context['extra'] ?? [],
            'environment' => Environment::get('APP_ENV', 'production'),
            'server_name' => gethostname(),
            'platform' => 'php',
            'sdk' => [
                'name' => 'roadrunner-sentry-transport',
                'version' => '1.0.0',
            ],
        ];
    }

    /**
     * Format stack trace for Sentry
     */
    private static function formatStackTrace(array $trace): array
    {
        return array_map(function ($frame) {
            return [
                'filename' => $frame['file'] ?? '<unknown>',
                'lineno' => $frame['line'] ?? 0,
                'function' => $frame['function'] ?? '<unknown>',
                'module' => $frame['class'] ?? null,
                'in_app' => !str_contains($frame['file'] ?? '', 'vendor/'),
            ];
        }, $trace);
    }
}

// Usage examples
if (php_sapi_name() === 'cli') {
    // Example usage
    $client = new SentryTransportClient();

    // Send a simple error
    try {
        throw new RuntimeException('Test error message');
    } catch (Throwable $e) {
        $event = SentryTransportClient::createErrorEvent($e, [
            'tags' => ['component' => 'example'],
            'extra' => ['user_id' => 123],
        ]);

        $result = $client->sendEvent($event);
        echo "Error event result: " . json_encode($result, JSON_PRETTY_PRINT) . "\n";
    }

    // Send a message
    $messageEvent = SentryTransportClient::createMessageEvent(
        'User logged in successfully',
        ['tags' => ['action' => 'login']],
        'info'
    );

    $result = $client->sendEvent($messageEvent);
    echo "Message event result: " . json_encode($result, JSON_PRETTY_PRINT) . "\n";

    // Send a batch of events
    $events = [
        SentryTransportClient::createMessageEvent('Event 1', [], 'info'),
        SentryTransportClient::createMessageEvent('Event 2', [], 'warning'),
        SentryTransportClient::createMessageEvent('Event 3', [], 'error'),
    ];

    $results = $client->sendBatch($events);
    echo "Batch results: " . json_encode($results, JSON_PRETTY_PRINT) . "\n";

    // Get status and metrics
    $status = $client->getStatus();
    echo "Plugin status: " . json_encode($status, JSON_PRETTY_PRINT) . "\n";

    $metrics = $client->getMetrics();
    echo "Plugin metrics: " . json_encode($metrics, JSON_PRETTY_PRINT) . "\n";
}
