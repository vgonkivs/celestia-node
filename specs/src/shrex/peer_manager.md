# PeerManager Specification

## Table of Contents

- [Abstract](#abstract)
- [Terminology](#terminology)
- [Overview](#overview)
- [Manager Structure](#manager-structure)
- [Peer Sources](#peer-sources)
- [Peer Selection Algorithm](#peer-selection-algorithm)
- [Peer Validation and State Management](#peer-validation-and-state-management)
- [Garbage Collection](#garbage-collection)
- [Configuration Parameters](#configuration-parameters)
- [API Reference](#api-reference)
- [Error Handling](#error-handling)
- [Metrics](#metrics)
- [Links](#links)

## Abstract

The PeerManager is a core component of Celestia's shrex (SHare REtrieval eXchange) system that handles peer discovery, selection, and lifecycle management for data availability sampling operations. It maintains pools of peers organized by data hashes, implements peer reputation management through cooldown and blacklisting mechanisms, and provides efficient peer selection using round-robin algorithms.

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT", "SHOULD", "SHOULD NOT", "RECOMMENDED", "MAY", and "OPTIONAL" in this document are to be interpreted as described in RFC 2119.

## Motivation

The PeerManager addresses critical challenges in decentralized data availability systems where nodes need to efficiently discover and select reliable peers for share retrieval operations.

### Problem Statement

In Celestia's data availability network, nodes must retrieve data shares from peers to perform data availability sampling (DAS). This presents several challenges:

1. **Peer Discovery Fragmentation**: Peers advertise specific data through shrex-sub, but these advertisements may not correspond to actual data availability
2. **Trust and Validation**: Not all peer advertisements can be trusted - some peers may advertise data they don't possess or provide invalid data
3. **Load Distribution**: Without proper peer selection, some peers may become overloaded while others remain underutilized
4. **Network Resilience**: The system needs to handle peer failures, disconnections, and malicious behavior gracefully
5. **Resource Management**: Memory and computational resources must be managed efficiently while maintaining recent peer information

### Solution Approach

The PeerManager provides a centralized solution that:

**Validates Peer Claims**: Cross-references shrex-sub advertisements with header subscriptions to ensure peers actually possess advertised data. Only peers whose claims are validated through blockchain headers are trusted for data retrieval.

**Implements Reputation Management**: Tracks peer behavior and implements graduated responses - from temporary cooldowns for minor failures to permanent blacklisting for malicious behavior. This protects the network from unreliable peers while allowing rehabilitation.

**Ensures Fair Load Distribution**: Uses round-robin selection within peer pools to distribute requests evenly across available peers, preventing overload and maximizing network throughput.

**Provides Fallback Mechanisms**: Maintains a discovery pool of general full nodes as backup when hash-specific peers are unavailable, ensuring service continuity.

**Manages Resource Efficiently**: Implements automatic garbage collection with height-based filtering to prevent memory bloat while maintaining peer information for recent data.

### Design Goals

- **Reliability**: Ensure data retrieval succeeds even under network stress or peer failures
- **Performance**: Minimize latency for peer selection while maximizing throughput
- **Security**: Protect against malicious peers and invalid data advertisements
- **Scalability**: Handle large numbers of peers and data hashes efficiently
- **Maintainability**: Provide clear separation of concerns and configurable behavior

## Terminology

- **Peer Pool**: A collection of peers that have advertised availability of specific data identified by a data hash
- **Data Hash**: A unique identifier for a piece of data (share) in the Celestia network
- **Cooldown**: A temporary state where a peer is unavailable for selection due to previous failures
- **Blacklisting**: A permanent exclusion of misbehaving peers from all future communications
- **Round-robin**: A selection algorithm that cycles through available peers to distribute load evenly
- **Shrex-sub**: A pub-sub mechanism for advertising share availability (see [Links](#links))
- **Discovery**: A peer discovery service for finding full nodes (see [Links](#links))
- **Validation Timeout**: The maximum time to wait for header confirmation of advertised data hashes

## Overview

The PeerManager serves as the central orchestrator for peer relationships in the shrex system. It addresses several key challenges:

1. **Dynamic Peer Discovery**: Automatically discovers peers through multiple sources
2. **Peer Quality Management**: Tracks peer behavior and maintains reputation scores
3. **Load Distribution**: Ensures even distribution of requests across healthy peers
4. **Data Integrity**: Validates that peers actually possess the data they advertise
5. **Resource Management**: Automatically cleans up stale peer information

The manager operates by maintaining separate pools of peers for each data hash, validating peer advertisements through header subscriptions, and providing peers on-demand with feedback mechanisms for continuous optimization.

## Manager Structure

The PeerManager is implemented as a stateful component with the following key characteristics:

- **Thread-safe operations**: All public methods are safe for concurrent access
- **Event-driven updates**: Responds to shrex-sub notifications and header events
- **Configurable behavior**: Supports multiple configuration options for different deployment scenarios
- **Resource cleanup**: Automatic garbage collection of outdated peer information

### Core Components

Based on the actual Manager struct definition, the core components are:

- **headerSub**: `libhead.Subscriber[*header.ExtendedHeader]` - Subscribes to new block headers to validate datahashes from shrex-sub notifications
- **shrexSub**: `*shrexsub.PubSub` - Receives peer notifications about data availability for specific data hashes via pub-sub
- **pools**: `map[string]*syncPool` - Hash-indexed map collecting peers from shrexSub, organized by datahash strings
- **nodes**: `*pool` - Pool collecting peer IDs discovered via the discovery service, used as fallback when shrex-sub peers unavailable
- **host**: `host.Host` - libp2p host for network operations and peer communication
- **connGater**: `*conngater.BasicConnectionGater` - Controls connection permissions, enables blocking blacklisted peers
- **blacklistedHashes**: `map[string]bool` - Map tracking datahashes that are not in the chain and should be rejected
- **params**: `Parameters` - Configuration including validation timeouts, cooldown durations, GC intervals, and blacklisting settings
- **tag**: `string` - Identifier for the type of peers this manager is handling
- **initialHeight**: `atomic.Uint64` - Height of the first header received from headerSub, used for validation boundaries
- **storeFrom**: `atomic.Uint64` - Messages from shrex.Sub with height below this value are ignored

## Peer Sources

The PeerManager aggregates peers from two primary sources:

### 1. Shrex-Sub Notifications
- **Primary source** for data-specific peers
- Peers advertise availability of specific data hashes
- Provides the most targeted peer selection
- Subject to validation through header subscriptions

### 2. Discovery Service
- **Fallback source** when shrex-sub peers are unavailable
- Provides general-purpose full nodes
- Less targeted but more reliable availability
- Used when no validated peers exist for requested data

### Peer Selection Priority
1. Validated peers from shrex-sub (highest priority)
2. Unvalidated peers from shrex-sub (medium priority)
3. Discovery peers (fallback priority)

## Peer Selection Algorithm

The PeerManager implements a round-robin selection strategy to return a different peer each time, ensuring fair load distribution across available peers.

### Selection Process
1. **Pool Identification**: Locate the peer pool for the requested data hash
2. **Availability Filtering**: Exclude cooled-down and blacklisted peers from selection
3. **Round-robin Selection**: Select the next available peer in rotation within the pool
4. **Fallback to Discovery**: If no shrex-sub peers are available, fall back to full nodes from discovery
5. **Blocking Wait**: If no peers exist in either source, wait until peers appear or timeout occurs

### Load Distribution Strategy
- Each data hash pool maintains independent round-robin state
- Selection cycles through all healthy peers in sequential order
- Ensures even distribution of requests across the peer set
- Prevents overloading individual peers while maximizing throughput
- Automatic failover maintains service availability during peer failures

## Peer Pools

### Pool Architecture
The PeerManager maintains a dual-pool system:

- **Hash-Specific Pools**: `map[string]*syncPool` indexed by datahash strings, containing peers that advertised specific data
- **Discovery Nodes Pool**: Single `*pool` containing general full nodes from discovery service as fallback
- **syncPool Enhancement**: Each hash-specific pool wraps a basic pool with validation state and metadata

### syncPool Structure
```go
type syncPool struct {
    *pool                           // Embedded pool with round-robin peer management
    isValidatedDataHash atomic.Bool // Whether corresponding header was received
    height             uint64       // Block height for this datahash
    createdAt          time.Time    // Creation timestamp for garbage collection
}
```

### Pool Lifecycle Management

1. **Pool Creation**: Triggered by shrex-sub notifications via `getOrCreatePool(datahash, height)`
2. **Validation Process**:
- Initially created as unvalidated (`isValidatedDataHash = false`)
- Marked validated when corresponding header arrives via `validatedPool(hashStr, height)`
- Validated pools promote all their peers to the discovery nodes pool
3. **Height-Based Storage**: Only pools for recent heights are maintained (controlled by `storedPoolsAmount = 10`)
4. **Automatic Cleanup**: Pools are garbage collected based on validation timeout and height thresholds

### Pool Validation States

**Unvalidated Pools**:
- Created when shrex-sub notification arrives but corresponding header not yet seen
- Peers in unvalidated pools are still available for selection
- Subject to garbage collection after `PoolValidationTimeout`

**Validated Pools**:
- Header subscription confirms the datahash exists in the blockchain
- All peers promoted to discovery nodes pool for broader availability
- Protected from timeout-based garbage collection
- Eventually cleaned up when height falls below `storeFrom` threshold

### Height-Based Management
```go
storeFrom := max(0, currentHeight - storedPoolsAmount)  // Only recent 10 heights
```
- **initialHeight**: First header height received, sets validation boundary
- **storeFrom**: Messages below this height are ignored to limit memory usage
- **Dynamic Updates**: `storeFrom` updates with each new header to maintain sliding window

## Validation Handler

### Handler Implementation
The `Validate` method serves as the pubsub validator for shrexsub notifications and is responsible for collecting peer IDs into corresponding peer pools:

```go
func (m *Manager) Validate(_ context.Context, peerID peer.ID, msg shrexsub.Notification) pubsub.ValidationResult
```

### Validation Process
The handler processes each incoming shrexsub notification through the following validation steps:

1. **Self-Message Bypass**: Messages broadcast from the manager's own host ID bypass validation with `ValidationAccept`
2. **Blacklist Filtering**:
- Rejects notifications for blacklisted data hashes with `ValidationReject`
- Rejects messages from blacklisted peers with `ValidationReject`
3. **Height Validation**: Ignores messages for headers below `storeFrom` threshold with `ValidationIgnore`
4. **Pool Management**: Creates or retrieves the appropriate syncPool for the data hash and adds the peer
5. **Discovery Integration**: If the pool's data hash is already validated, adds the peer to the discovery nodes pool

### Validation Results
- **ValidationAccept**: Used only for self-messages
- **ValidationReject**: Applied to blacklisted hashes or peers, triggers pubsub-level rejection
- **ValidationIgnore**: Used for valid messages that should be processed but not propagated further

### syncPool Structure
Each pool created for datahashes has the following characteristics:

```go
type syncPool struct {
    *pool
    isValidatedDataHash atomic.Bool  // Indicates if corresponding header was received
    height             uint64        // Height of the header for this datahash
    createdAt          time.Time     // Pool creation timestamp for GC purposes
}
```

### Header Correlation System
The validation system operates through a two-subscription model:

- **Shrex-Sub Notifications**: Create unvalidated pools when peers advertise data availability
- **Header Subscription**: The `subscribeHeader` goroutine marks pools as validated when corresponding headers arrive
- **Validation Timeout**: Unvalidated pools are garbage collected after `PoolValidationTimeout` expires
- **Peer Promotion**: When a pool becomes validated, all its peers are added to the discovery nodes pool

## Garbage Collection

The PeerManager implements automatic cleanup mechanisms:

### Unvalidated Pool Cleanup
- Pools created from shrex-sub advertisements without header confirmation
- Cleaned up after `PoolValidationTimeout` expires
- Prevents memory leaks from invalid advertisements

### Cooldown Expiration
- Peers on cooldown are automatically returned to active status
- Cleanup occurs during regular GC intervals
- Balances peer rehabilitation with system performance

### Configuration
- **GcInterval**: How frequently garbage collection runs
- **PoolValidationTimeout**: Maximum time to wait for header validation

## Configuration Parameters

### Parameters Structure
```go
type Parameters struct {
    PoolValidationTimeout time.Duration  // Timeout for validating datahashes
    PeerCooldown         time.Duration  // Duration for cooldown state
    GcInterval           time.Duration  // Garbage collection frequency
    EnableBlackListing   bool          // Enable permanent peer blacklisting
}
```

### Key Configuration Options

- **PoolValidationTimeout**: Controls how long to wait for header validation of shrex-sub advertisements
- **PeerCooldown**: Duration that peers remain unavailable after being marked for cooldown
- **GcInterval**: Frequency of automatic cleanup operations
- **EnableBlackListing**: Whether to permanently block misbehaving peers

## API Reference

### Constructor
```go
func NewManager(
    params Parameters,
    host host.Host,
    connGater *conngater.BasicConnectionGater,
    tag string,
    options ...Option,
) (*Manager, error)
```

### Core Methods

#### Peer Selection
```go
func (m *Manager) Peer(
    ctx context.Context,
    datahash share.DataHash,
    height uint64,
) (peer.ID, DoneFunc, error)
```
Returns a peer for the specified data hash. Includes a callback function for reporting operation results.

#### Lifecycle Management
```go
func (m *Manager) Start(startCtx context.Context) error
```
Starts the peer manager's background processes including validation and garbage collection.

#### Validation Handler
```go
func (m *Manager) Validate(
    _ context.Context,
    peerID peer.ID,
    msg shrexsub.Notification,
) pubsub.ValidationResult
```
Processes incoming shrex-sub notifications and maintains peer pools.

#### Discovery Integration
```go
func (m *Manager) UpdateNodePool(peerID peer.ID, isAdded bool)
```
Updates the discovery peer pool when nodes are discovered or removed.

### Option Functions

#### Shrex-Sub Integration
```go
func WithShrexSubPools(
    shrexSub *shrexsub.PubSub,
    headerSub libhead.Subscriber[*header.ExtendedHeader],
) Option
```

## Links

- [Shrex-Sub Specification](link-to-shrex-sub-spec) - Pub-sub mechanism for share advertisements
- [Discovery Specification](link-to-discovery-spec) - Peer discovery service for full nodes
- [Shrex Getter Specification](link-to-shrex-getter-spec) - Data retrieval component that uses PeerManager

---

**TODO**:
- Add detailed message format specifications
- Include sequence diagrams for peer lifecycle
- Add performance benchmarks and capacity planning guidelines
- Document integration patterns with other shrex components