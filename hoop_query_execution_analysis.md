# Hoop Query Execution Issue Analysis

## Problem Summary

Based on the Slack thread, users are experiencing issues where queries in Hoop are not executing and getting stuck ("Estou tentando executar as querys no hoop, mas elas não executam e ficam travadas la"). This is a critical issue that affects the core functionality of the Hoop.dev access gateway.

## Root Cause Analysis

After analyzing the Hoop codebase, I've identified several potential root causes for queries getting stuck:

### 1. **Connection Pool Timeout Issues**

**Location**: `gateway/transport/dispatchers.go`

The dispatcher system has multiple timeout mechanisms that could cause queries to hang:

- **10-second timeout** for opening sessions (`DispatchOpenSession`)
- **2-second timeout** for sending responses back to API clients
- **500ms timeout** for dispatching session requests

```go
// From dispatchers.go:98
case <-time.After(time.Second * 10):
    return nil, fmt.Errorf("timeout (10s) waiting to open a session")
```

### 2. **Proxy Session Management**

**Location**: `gateway/transport/streamclient/proxy.go`

- **48-hour maximum timeout** for session cleanup
- Proxy streams can get stuck waiting for session cleanup
- Memory cleanup process runs only every 15 minutes

```go
// From proxy.go:45
proxyMaxTimeoutDuration = time.Hour * 48
```

### 3. **Database Connection Layer Issues**

**Location**: `agent/controller/postgres.go`, `agent/controller/mysql.go`

The database connection handling through `libhoop.NewDBCore()` lacks explicit timeout configurations:

- No connection timeout specified in database connection options
- No query timeout set at the database level
- Connections can hang indefinitely waiting for database responses

### 4. **Agent-Gateway Communication**

**Location**: `agent/controller/agent.go`

- **5-second TCP liveness check** timeout
- **10-second TCP connection timeout** for new connections
- No explicit query timeout in database protocol handlers

## Technical Deep Dive

### Connection Flow Analysis

1. **Client Request** → **Gateway** → **Agent** → **Database**
2. Each layer has its own timeout configurations
3. If any layer hangs, the entire query chain gets stuck

### Timeout Hierarchy

```
Client Request
├── Gateway Dispatcher (10s session timeout)
├── Proxy Stream (48h max session)
├── Agent Controller (5s TCP check)
└── Database Connection (NO TIMEOUT ⚠️)
```

## Evidence from Codebase

### Missing Database Timeouts

In `agent/controller/postgres.go` and `mysql.go`, the database connection options don't include essential timeout parameters:

```go
opts := map[string]string{
    "sid":      sessionID,
    "hostname": connenv.host,
    "port":     connenv.port,
    "username": connenv.user,
    "password": connenv.pass,
    // Missing: query_timeout, connection_timeout, read_timeout
}
```

### Connection Pool Issues

The agent uses a basic in-memory store (`memory.Store`) for connection management without sophisticated pool management or timeout handling.

### Critical Discovery: LibHoop No-Op Implementation

**Location**: `_libhoop/libhoop.go`

**Critical Issue**: The `libhoop` package appears to be using no-op (no operation) implementations for all database protocols:

```go
func (p *noopProxy) Run(onErr func(int, string)) {
    errMsg := fmt.Sprintf("missing protocol hoop library for %v, contact your administrator", p.connectionType)
    onErr(1, errMsg)
}
```

This means:
- All database connections return a "missing protocol hoop library" error
- No actual database communication is happening
- Queries appear to be stuck because they're not actually executing

**This could be the primary cause of the stuck query issue** - the actual database protocol implementations may be missing or not properly linked.

## Recommendations

### Immediate Fixes (High Priority)

1. **🚨 CRITICAL: Investigate LibHoop Implementation**
   - The `_libhoop` directory contains only no-op implementations
   - Actual database protocol implementations may be missing
   - Check if there's a separate proprietary/commercial library that needs to be linked
   - Verify the build process includes the correct database protocol libraries

2. **Add Database Query Timeouts**
   - Implement query timeouts in `libhoop` database connections
   - Add configurable timeout parameters to connection options
   - Default to 30-60 seconds for most queries

3. **Improve Connection Pool Management**
   - Add connection validation before reuse
   - Implement connection health checks
   - Add configurable connection lifetime limits

4. **Enhanced Error Handling**
   - Add proper timeout error messages
   - Implement graceful degradation when timeouts occur
   - Log timeout events for debugging

### Medium-term Improvements

1. **Connection Pool Optimization**
   - Implement proper connection pooling with min/max connections
   - Add connection idle timeout management
   - Implement connection retry logic with exponential backoff

2. **Monitoring and Observability**
   - Add metrics for connection pool usage
   - Track query execution times
   - Monitor timeout occurrences

3. **Configuration Management**
   - Make timeouts configurable via environment variables
   - Provide different timeout profiles for different connection types
   - Allow per-connection timeout customization

### Long-term Architectural Changes

1. **Circuit Breaker Pattern**
   - Implement circuit breakers for database connections
   - Automatic failover when connections consistently timeout
   - Health check endpoints for connection status

2. **Advanced Connection Management**
   - Connection multiplexing for better resource utilization
   - Dynamic connection scaling based on load
   - Connection affinity for improved performance

## Implementation Priority

### Phase 1: Critical Fixes
- [ ] Add database query timeouts (1-2 days)
- [ ] Implement connection validation (1 day)
- [ ] Improve error messages (1 day)

### Phase 2: Stability Improvements
- [ ] Enhanced connection pool management (1 week)
- [ ] Monitoring and metrics (3-5 days)
- [ ] Configuration management (2-3 days)

### Phase 3: Advanced Features
- [ ] Circuit breaker implementation (1-2 weeks)
- [ ] Advanced connection management (2-3 weeks)

## Specific Code Changes Needed

### 1. Database Connection Timeouts

**File**: `agent/controller/postgres.go`
```go
opts := map[string]string{
    "sid":             sessionID,
    "hostname":        connenv.host,
    "port":           connenv.port,
    "username":       connenv.user,
    "password":       connenv.pass,
    "query_timeout":  "30s",        // Add this
    "connect_timeout": "10s",       // Add this
    "read_timeout":   "30s",        // Add this
}
```

### 2. Connection Validation

**File**: `agent/controller/agent.go`
```go
func (a *Agent) validateConnection(sessionID string) error {
    // Add connection health check before query execution
    // Implement ping/validation logic
}
```

### 3. Timeout Configuration

**File**: `agent/config/config.go`
```go
type Config struct {
    // Existing fields...
    DatabaseQueryTimeout   time.Duration `env:"DB_QUERY_TIMEOUT" envDefault:"30s"`
    DatabaseConnectTimeout time.Duration `env:"DB_CONNECT_TIMEOUT" envDefault:"10s"`
    DatabaseReadTimeout    time.Duration `env:"DB_READ_TIMEOUT" envDefault:"30s"`
}
```

## Conclusion

The stuck query issue in Hoop appears to have two potential root causes:

1. **PRIMARY SUSPECT**: The `libhoop` package contains only no-op implementations for all database protocols, which means queries may appear "stuck" because they're not actually executing. This requires immediate investigation to determine if actual database protocol implementations are missing.

2. **SECONDARY ISSUES**: Missing database-level timeouts and inadequate connection pool management could contribute to the problem even if the protocol implementations are correctly linked.

**Immediate Action Required**: Verify that the proper database protocol libraries are linked and functioning. The no-op implementations in `_libhoop/libhoop.go` suggest that either:
- The build process is not including the correct libraries
- There's a separate commercial/proprietary component that needs to be installed
- The open-source version may have limited database protocol support

The recommended approach is to first resolve the protocol implementation issue, then implement the timeout and connection management improvements for long-term stability.